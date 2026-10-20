// Package banners is the staff desk for the site-wide promotional strip; it
// holds writing a strip in both languages, its run and switching it on or off;
// it does not hold the strip as shoppers see it (site) or the home carousel's
// slides (slides).
package banners

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
)

var (
	ErrNotFound = errors.New("banners: not found")
	// ErrRefused is a write the database declined; its message is the database's
	// own, because that names the rule.
	ErrRefused = errors.New("banners: refused")
)

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("banners: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}
