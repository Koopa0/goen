package warranty

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/shoptime"
)

type Store struct {
	q *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("warranty: NewStore requires a pool")
	}
	return &Store{q: db.New(pool)}
}

func (s *Store) Registrable(ctx context.Context, orderNumber, userID string) (OrderView, error) {
	owner, err := uuid.Parse(userID)
	if err != nil {
		return OrderView{}, ErrNotFound
	}
	rows, err := s.q.RegistrableLines(ctx, db.RegistrableLinesParams{
		OrderNumber: pgtype.Text{String: orderNumber, Valid: true}, UserID: uuid.NullUUID{UUID: owner, Valid: true},
	})
	if err != nil {
		return OrderView{}, fmt.Errorf("read registrable lines: %w", err)
	}
	if len(rows) == 0 {
		return OrderView{}, ErrNotFound
	}

	now := time.Now()
	view := OrderView{Number: orderNumber}
	for i := range rows {
		r := &rows[i]
		view.Lines = append(view.Lines, Line{
			ID: r.OrderLineID.String(), Name: r.ProductName,
			Label: r.VariantLabel.String, Slug: r.ProductSlug.String,
			Note: r.WarrantyNote, Months: int(r.WarrantyMonths.Int32),
			HasTerm:   r.WarrantyMonths.Valid,
			Delivered: int(r.DeliveredUnits), Returned: int(r.ReturnedUnits),
			Registered: int(r.RegisteredUnits),
		})
		view.Lines[len(view.Lines)-1].CoverEnds = warrantyCoverEnd(r.ExpiresOn, r.HasExpiry, now)
	}
	registered, err := s.Mine(ctx, userID)
	if err != nil {
		return OrderView{}, err
	}
	lastUnits := make(map[string]int)
	for i := range registered {
		item := &registered[i]
		if item.Order == orderNumber {
			view.Registered = append(view.Registered, *item)
			for i := range view.Lines {
				line := &view.Lines[i]
				if line.ID == item.LineID && line.Remaining() == 0 && item.Unit > lastUnits[line.ID] {
					day := item.ExpiresOn
					line.CoverEnds = &day
					lastUnits[line.ID] = item.Unit
				}
			}
		}
	}
	return view, nil
}

func warrantyLine(r *db.RegistrableLinesRow, now time.Time) Line {
	line := Line{
		ID: r.OrderLineID.String(), Name: r.ProductName,
		Label: r.VariantLabel.String, Slug: r.ProductSlug.String,
		Note: r.WarrantyNote, Months: int(r.WarrantyMonths.Int32),
		HasTerm:   r.WarrantyMonths.Valid,
		Delivered: int(r.DeliveredUnits), Returned: int(r.ReturnedUnits),
		Registered: int(r.RegisteredUnits),
		CoverEnds:  warrantyCoverEnd(r.ExpiresOn, r.HasExpiry, now),
	}
	return line
}

func warrantyCoverEnd(date time.Time, hasExpiry bool, now time.Time) *shoptime.Date {
	if !hasExpiry {
		return nil
	}
	day := shoptime.DateOf(date, now)
	return &day
}

func (s *Store) Ready(ctx context.Context, userID string) ([]Ready, error) {
	owner, err := uuid.Parse(userID)
	if err != nil {
		return nil, ErrNotFound
	}
	rows, err := s.q.RegistrableLines(ctx, db.RegistrableLinesParams{
		UserID: uuid.NullUUID{UUID: owner, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	now := time.Now()
	ready := make([]Ready, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		line := warrantyLine(row, now)
		if line.Registrable() && line.CoverEnds != nil {
			ready = append(ready, Ready{Number: row.OrderNumber, Line: line, DeliveredOn: shoptime.DateOf(row.DeliveredOn, now)})
		}
	}
	return ready, nil
}

// Register decides ownership, delivery and the term in the statement's WHERE
// clause, under the same read the insert uses.
func (s *Store) Register(ctx context.Context, orderNumber, lineID, userID, serial string, unit int) error {
	owner, err := uuid.Parse(userID)
	if err != nil {
		return ErrNotFound
	}
	line, err := uuid.Parse(lineID)
	if err != nil {
		return ErrInvalid
	}
	serial = strings.TrimSpace(serial)
	if utf8.RuneCountInString(serial) > MaxSerialRunes {
		return ErrSerialTooLong
	}
	if unit < 1 || unit > maxUnits {
		return ErrInvalid
	}

	n, err := s.q.RegisterWarranty(ctx, db.RegisterWarrantyParams{
		OrderNumber:  orderNumber,
		OrderLineID:  line,
		UnitNo:       int16(unit),
		UserID:       uuid.NullUUID{UUID: owner, Valid: true},
		SerialNumber: serial,
	})
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
			switch pgErr.ConstraintName {
			case "warranty_registrations_serial_key":
				return ErrSerialTaken
			case "warranty_registrations_unit_key":
				return ErrNotRegistrable
			case "warranty_registrations_serial_length":
				return ErrSerialTooLong
			case "warranty_unit_within_purchase":
				return ErrNotRegistrable
			}
		}
		return fmt.Errorf("register warranty: %w", err)
	}
	if n == 0 {
		return ErrNotRegistrable
	}
	return nil
}

func (s *Store) Mine(ctx context.Context, userID string) ([]Warranty, error) {
	owner, err := uuid.Parse(userID)
	if err != nil {
		return nil, nil //nolint:nilerr // an unparseable id has registered nothing
	}
	rows, err := s.q.MyWarranties(ctx, uuid.NullUUID{UUID: owner, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("read warranties: %w", err)
	}
	orderIDs := make([]uuid.UUID, len(rows))
	for i := range rows {
		orderIDs[i] = rows[i].OrderID
	}
	returned, err := s.q.ReturnedOrders(ctx, orderIDs)
	if err != nil {
		return nil, fmt.Errorf("read returned orders: %w", err)
	}
	now := time.Now()
	out := make([]Warranty, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		gone := slices.Contains(returned, r.OrderID)
		out = append(out, Warranty{
			LineID: r.OrderLineID.String(),
			Name:   r.ProductName, Label: r.VariantLabel.String,
			Slug: r.ProductSlug, Order: r.OrderNumber,
			Unit: int(r.UnitNo), Serial: r.SerialNumber,
			RegisteredAt: shoptime.DateText(ctx, shoptime.DateOf(r.RegisteredAt, now)),
			ExpiresOn:    shoptime.DateOf(r.ExpiresOn, now),
			InForce:      r.InForce && !gone,
			Returned:     gone,
		})
	}
	return out, nil
}

// maxUnits keeps an absurd unit number out of the smallint cast;
// warranty_unit_within_purchase decides the real ceiling.
const maxUnits = 1000
