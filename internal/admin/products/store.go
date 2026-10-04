// Package products is the back office's catalogue desk: the product list and
// form, each product's variants, option axes, specs and images.
package products

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
)

var (
	ErrNotFound = errors.New("products: not found")
	// ErrRefused is a write the database declined; its message is the database's
	// own, because that names the rule.
	ErrRefused = errors.New("products: refused")
	ErrInvalid = errors.New("products: invalid input")
)

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("products: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}
