package contact

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/db"
)

// errDuplicate stays package-private: no caller has a distinct duplicate
// branch.
var errDuplicate = errors.New("contact: message already recorded")

type Store struct {
	q *db.Queries
}

func NewStore(dbtx db.DBTX) *Store {
	if dbtx == nil {
		panic("contact: NewStore requires a database handle")
	}
	return &Store{q: db.New(dbtx)}
}

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

func wrap(op string, err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.Code {
		case "23505": // unique_violation
			return fmt.Errorf("%s: %w", op, errDuplicate)
		case "23514", "23503": // check_violation, foreign_key_violation
			return fmt.Errorf("%s: rejected by %s: %w", op, pgErr.ConstraintName, err)
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}

// optionalText maps an omitted field to SQL NULL, so "no order reference" and
// "an empty one" are not the same row.
func optionalText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}
