// Package slides is the staff desk for the home carousel's scheduled slides; it
// holds creating a slide, its schedule, switching one on or off and promoting
// it, beside a preview of the carousel as shoppers see it now; it does not hold
// the campaigns and departments that also fill the carousel (campaigns,
// taxonomy) or the site-wide promotional strip (banners).
package slides

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/components"
)

var (
	ErrNotFound = errors.New("slides: not found")
	// ErrRefused is a write the database declined; its message is the database's
	// own, because that names the rule.
	ErrRefused = errors.New("slides: refused")
)

// Storefront builds the home page's carousel as shoppers see it now; the
// storefront's home store implements it. The desk lists those slides beside its
// schedule instead of keeping a copy of the rules that choose them.
type Storefront interface {
	Carousel(ctx context.Context) ([]components.HeroSlide, error)
}

type Store struct {
	pool       *pgxpool.Pool
	q          *db.Queries
	storefront Storefront
}

func NewStore(pool *pgxpool.Pool, storefront Storefront) *Store {
	if pool == nil || storefront == nil {
		panic("slides: NewStore requires a pool and a storefront")
	}
	return &Store{pool: pool, q: db.New(pool), storefront: storefront}
}
