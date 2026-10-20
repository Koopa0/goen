// Package faq is the staff desk that edits the shop's frequently asked
// questions; it holds the FAQ's categories and entries, in both languages, and
// their order; it does not hold the storefront's FAQ page (site) or any other
// shop-window copy (slides, banners).
package faq

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
)

var (
	ErrNotFound = errors.New("faq: not found")
	// ErrRefused is a write the database declined; its message is the database's
	// own, because that names the rule.
	ErrRefused = errors.New("faq: refused")
)

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("faq: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}
