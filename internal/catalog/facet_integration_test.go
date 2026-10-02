//go:build integration

package catalog_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/catalog"
)

// A brand's count is what choosing it would show with the other filters kept,
// and a chosen brand the other filters empty stays listed so it can be unchosen.
func TestBrandCountsFollowTheOtherFiltersAndNotTheBrandFilter(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(ctx)) })

	category := "facet-" + uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO categories (slug, name, position)
   SELECT $1, 'facet fixture', coalesce(max(position) + 1, 0) FROM categories WHERE parent_id IS NULL`, category); err != nil {
		t.Fatal(err)
	}
	brands := map[string]string{"cheap": "facet-cheap-" + uuid.NewString(), "dear": "facet-dear-" + uuid.NewString()}
	for kind, slug := range brands {
		price := 10000
		if kind == "dear" {
			price = 90000
		}
		// Two cheap or two dear products, so a count of 2 cannot be a count of 1.
		for range 2 {
			var productID uuid.UUID
			if err = tx.QueryRow(ctx, `WITH b AS (
   INSERT INTO brands (slug, name) VALUES ($1, $1) ON CONFLICT (slug) DO UPDATE SET name = brands.name RETURNING id)
   INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
   SELECT b.id, c.id, $3, 'facet product', 'draft', now() FROM b, categories c WHERE c.slug = $2 RETURNING id`,
				slug, category, "facet-p-"+uuid.NewString()).Scan(&productID); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO product_variants (product_id, sku, price_cents) VALUES ($1, $2, $3)`,
				productID, "FACET-"+uuid.NewString(), price); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `UPDATE products SET status = 'active' WHERE id = $1`, productID); err != nil {
				t.Fatal(err)
			}
		}
	}

	store := catalog.NewStore(tx)
	counts := func(f catalog.Filters) map[string]int64 {
		t.Helper()
		view, listErr := store.Listing(ctx, category, f)
		if listErr != nil {
			t.Fatal(listErr)
		}
		out := map[string]int64{}
		for _, b := range view.Brands {
			out[b.Value] = b.Count
		}
		return out
	}
	cheap, dear := brands["cheap"], brands["dear"]

	if got := counts(catalog.Filters{Page: 1}); got[cheap] != 2 || got[dear] != 2 {
		t.Errorf("unfiltered counts = %v, want 2 and 2", got)
	}
	// A price ceiling empties the dear brand without removing it from the panel.
	if got := counts(catalog.Filters{MaxPrice: 50000, Page: 1}); got[cheap] != 2 || got[dear] != 0 {
		t.Errorf("under NT$500 counts = %v, want cheap 2 and dear 0", got)
	}
	// The brand filter itself never narrows the counts, or a second brand could not be added.
	if got := counts(catalog.Filters{BrandSlugs: []string{cheap}, Page: 1}); got[cheap] != 2 || got[dear] != 2 {
		t.Errorf("with the cheap brand chosen counts = %v, want 2 and 2", got)
	}
	if got := counts(catalog.Filters{BrandSlugs: []string{dear}, MaxPrice: 50000, Page: 1}); got[dear] != 0 || got[cheap] != 2 {
		t.Errorf("a chosen brand the price empties: counts = %v, want dear 0 listed and cheap 2", got)
	}
}
