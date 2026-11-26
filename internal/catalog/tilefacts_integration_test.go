//go:build integration

package catalog_test

import (
	"log/slog"
	"testing"

	"github.com/koopa0/goen/internal/catalog"
)

// TestStore_ListingHighlights holds that a shelf gives each tile its
// specification highlights, at most three; this product lists two.
func TestStore_ListingHighlights(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	slug, _ := twoProductsWithSpecs(t)
	var dept string
	if err := pool.QueryRow(ctx, `
		SELECT c.slug FROM products p JOIN categories c ON c.id = p.category_id WHERE p.slug = $1`,
		slug).Scan(&dept); err != nil {
		t.Fatalf("category of %s: %v", slug, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE categories SET comparable = true WHERE slug = $1`, dept); err != nil {
		t.Fatalf("make the department compare: %v", err)
	}
	view, err := s.Listing(ctx, dept, catalog.Filters{})
	if err != nil {
		t.Fatalf("Listing: %v", err)
	}
	if len(view.Products) != 1 {
		t.Fatalf("shelf holds %d products, want the one product with specifications", len(view.Products))
	}
	if n := len(view.Products[0].Highlights); n != 2 {
		t.Errorf("tile has %d highlights, want its 2 specifications", n)
	}
}
