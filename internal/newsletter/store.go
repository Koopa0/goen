package newsletter

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/db"
)

// Store records subscriptions in PostgreSQL.
type Store struct {
	q *db.Queries
}

// NewStore returns a Store reading and writing through dbtx.
func NewStore(dbtx db.DBTX) *Store {
	if dbtx == nil {
		panic("newsletter: NewStore requires a database handle")
	}
	return &Store{q: db.New(dbtx)}
}

// WithTx returns a Store whose writes join tx.
func (s *Store) WithTx(tx pgx.Tx) *Store {
	return &Store{q: db.New(tx)}
}

// Subscribe records addr as subscribed. Subscribing an address that is already
// on the list succeeds: the query clears any prior opt-out rather than
// conflicting, so a visitor who submits twice is not shown a failure for
// something that is not one.
func (s *Store) Subscribe(ctx context.Context, addr string) error {
	if err := s.q.SubscribeNewsletter(ctx, addr); err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23514" {
			return fmt.Errorf("subscribe newsletter: rejected by %s: %w", pgErr.ConstraintName, err)
		}
		return fmt.Errorf("subscribe newsletter: %w", err)
	}
	return nil
}
