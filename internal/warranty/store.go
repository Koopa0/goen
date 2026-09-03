package warranty

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Store is the database side of warranty registration.
type Store struct {
	q *db.Queries
}

// NewStore returns a Store over pool.
func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("warranty: NewStore requires a pool")
	}
	return &Store{q: db.New(pool)}
}

// Registrable is what a customer may still register on one order.
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
			Delivered: int(r.DeliveredUnits), Registered: int(r.RegisteredUnits),
		})
	}
	return view, nil
}

// Register records cover for one unit. Ownership, delivery and the term are all
// in the statement's WHERE clause, so they are decided under the same read the
// insert uses.
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
		return ErrInvalid
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
			case "warranty_unit_within_purchase":
				return ErrNotRegistrable
			}
		}
		return fmt.Errorf("register warranty: %w", err)
	}
	if n == 0 {
		// The WHERE clause refused: not owned, not delivered, or no term set.
		return ErrNotRegistrable
	}
	return nil
}

// Mine is what this customer has registered.
func (s *Store) Mine(ctx context.Context, userID string) ([]pages.Warranty, error) {
	owner, err := uuid.Parse(userID)
	if err != nil {
		return nil, nil //nolint:nilerr // an unparseable id has registered nothing
	}
	rows, err := s.q.MyWarranties(ctx, uuid.NullUUID{UUID: owner, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("read warranties: %w", err)
	}
	out := make([]pages.Warranty, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, pages.Warranty{
			Name: r.ProductName, Label: r.VariantLabel.String,
			Slug: r.ProductSlug, Order: r.OrderNumber,
			Unit: int(r.UnitNo), Serial: r.SerialNumber,
			RegisteredAt: shoptime.Day(r.RegisteredAt),
			ExpiresOn:    shoptime.Day(r.ExpiresOn),
			InForce:      r.InForce,
		})
	}
	return out, nil
}

// maxUnits keeps an absurd unit number out of the smallint cast;
// warranty_unit_within_purchase decides the real ceiling.
const maxUnits = 1000
