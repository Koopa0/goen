// Package newsletters is the staff desk that composes and sends newsletter
// issues; it holds the composer, the list of issues, each issue's one send
// through the outbox and the mailing list's counts; it does not hold the
// subscriber list or the sign-up (newsletter).
package newsletters

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
)

var ErrNotFound = errors.New("newsletters: not found")

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("newsletters: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}
