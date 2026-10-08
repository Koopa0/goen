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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
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

func (s *Store) Registrable(ctx context.Context, orderNumber, userID string) (pages.WarrantyOrderView, error) {
	owner, err := uuid.Parse(userID)
	if err != nil {
		return pages.WarrantyOrderView{}, ErrNotFound
	}
	rows, err := s.q.RegistrableLines(ctx, db.RegistrableLinesParams{
		OrderNumber: orderNumber, UserID: uuid.NullUUID{UUID: owner, Valid: true},
	})
	if err != nil {
		return pages.WarrantyOrderView{}, fmt.Errorf("read registrable lines: %w", err)
	}
	if len(rows) == 0 {
		return pages.WarrantyOrderView{}, ErrNotFound
	}

	view := pages.WarrantyOrderView{Number: orderNumber}
	for i := range rows {
		r := &rows[i]
		view.Lines = append(view.Lines, pages.WarrantyLine{
			ID: r.OrderLineID.String(), Name: r.ProductName,
			Label: r.VariantLabel.String, Slug: r.ProductSlug.String,
			Note: r.WarrantyNote, Months: int(r.WarrantyMonths.Int32),
			HasTerm:   r.WarrantyMonths.Valid,
			Delivered: int(r.DeliveredUnits), Returned: int(r.ReturnedUnits),
			Registered: int(r.RegisteredUnits),
		})
	}
	return view, nil
}

// Register decides ownership, delivery and the term in the statement's WHERE
// clause, under the same read the insert uses.
func (s *Store) Register(ctx context.Context, lineID, userID, serial string, unit int) error {
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

func (s *Store) Mine(ctx context.Context, userID string) ([]pages.Warranty, error) {
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
	out := make([]pages.Warranty, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		gone := slices.Contains(returned, r.OrderID)
		out = append(out, pages.Warranty{
			Name: r.ProductName, Label: r.VariantLabel.String,
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
