// Package home renders goen's storefront home page: the top-level category
// tiles and the recommended products, read live. The read model is per-view
// aggregation by measurement — see docs/decisions/001-home-read-model.md.
package home

import (
	"context"
	"fmt"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Store reads the home page's data.
type Store struct {
	q *db.Queries
}

// NewStore returns a Store reading through dbtx.
func NewStore(dbtx db.DBTX) *Store {
	if dbtx == nil {
		panic("home: NewStore requires a database handle")
	}
	return &Store{q: db.New(dbtx)}
}

// Load reads the category tiles and the top recommended product tiles. It
// returns view types, never the generated db rows.
func (s *Store) Load(ctx context.Context, recommended int32) (pages.HomeView, error) {
	hero, err := s.Hero(ctx)
	if err != nil {
		return pages.HomeView{}, err
	}
	cats, err := s.q.HomeCategories(ctx, string(i18n.FromContext(ctx)))
	if err != nil {
		return pages.HomeView{}, fmt.Errorf("read home categories: %w", err)
	}
	tiles, err := s.q.HomeRecommendedTiles(ctx, db.HomeRecommendedTilesParams{
		Limit: recommended, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return pages.HomeView{}, fmt.Errorf("read home tiles: %w", err)
	}

	// The trust strip states what it takes to get free delivery, and that figure
	// is the shop's to edit at /admin/shipping. Read rather than typed, for the
	// reason ShippingPolicy gives: a page that restates a promise can drift from
	// the till, and this one had the number written into the i18n catalogue.
	freeOver, err := s.q.FreeDeliveryThreshold(ctx)
	if err != nil {
		return pages.HomeView{}, fmt.Errorf("read free delivery threshold: %w", err)
	}

	view := pages.HomeView{
		Hero:              hero,
		Categories:        make([]pages.HomeCategory, 0, len(cats)),
		Recommended:       make([]pages.ProductTile, 0, len(tiles)),
		FreeDeliveryCents: freeOver,
	}
	for _, c := range cats {
		view.Categories = append(view.Categories, pages.HomeCategory{
			Slug:    c.Slug,
			Name:    c.Name,
			IconKey: c.IconKey.String,
		})
	}
	for i := range tiles {
		t := &tiles[i]
		view.Recommended = append(view.Recommended, pages.ProductTile{
			Slug:         t.Slug,
			Name:         t.Name,
			Summary:      t.Summary,
			Brand:        t.Brand,
			PriceCents:   t.MinPriceCents,
			CompareCents: t.CompareAtPriceCents.Int64, // 0 when NULL
			Rating:       t.Rating,
			RatingCount:  t.RatingCount,
			InStock:      t.InStock,
			ImageURL:     assets.ProductImageURL(t.ImageKey),
			ImageSrcset:  assets.ProductImageSrcsetAt(t.ImageKey, int(t.ImageWidth)),
			ImageAlt:     t.ImageAlt,
			ImageWidth:   t.ImageWidth,
			ImageHeight:  t.ImageHeight,
		})
	}
	return view, nil
}
