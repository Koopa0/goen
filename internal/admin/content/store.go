// Package content is the back office's shop-window copy: the home page's hero
// slides and promo banner, the FAQ, and the newsletter composer.
package content

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
)

var (
	ErrNotFound = errors.New("content: not found")
	// ErrRefused is a write the database declined; its message is the database's
	// own, because that names the rule.
	ErrRefused = errors.New("content: refused")
)

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("content: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}
