package contact

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/db"
)

// ErrDuplicate reports that an equivalent message is already stored. It exists
// so a caller can tell "we already have this" from "the database is down"
// without reading a driver error code.
var ErrDuplicate = errors.New("contact: message already recorded")

// Store records contact messages in PostgreSQL.
//
// It holds a [db.DBTX] rather than a pool, so the same code serves a pooled
// call and a call inside a transaction — see [Store.WithTx].
type Store struct {
	q *db.Queries
}

// NewStore returns a Store reading and writing through dbtx.
func NewStore(dbtx db.DBTX) *Store {
	if dbtx == nil {
		panic("contact: NewStore requires a database handle")
	}
	return &Store{q: db.New(dbtx)}
}

// WithTx returns a Store whose writes join tx, so a caller composing several
// features into one transaction does not have to reach past this package to do
// it.
func (s *Store) WithTx(tx pgx.Tx) *Store {
	return &Store{q: db.New(tx)}
}

// Create stores a validated message.
func (s *Store) Create(ctx context.Context, m Message) error {
	_, err := s.q.CreateContactMessage(ctx, db.CreateContactMessageParams{
		Name:     m.Name,
		Email:    m.Email,
		Subject:  m.Subject,
		OrderRef: optionalText(m.OrderRef),
		Message:  m.Body,
	})
	if err != nil {
		return wrap("create contact message", err)
	}
	return nil
}

// wrap turns a driver error into something this package's callers can act on.
// A unique violation is a fact about the request, not a failure of the system,
// and only this layer knows which SQLSTATE means which.
func wrap(op string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", op, pgx.ErrNoRows)
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.Code {
		case "23505": // unique_violation
			return fmt.Errorf("%s: %w", op, ErrDuplicate)
		case "23514", "23503": // check_violation, foreign_key_violation
			return fmt.Errorf("%s: rejected by %s: %w", op, pgErr.ConstraintName, err)
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}

// optionalText maps an omitted field to SQL NULL, so "no order reference" and
// "an order reference that is the empty string" do not become the same row.
func optionalText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}
