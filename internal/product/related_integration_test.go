//go:build integration

package product_test

import (
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/product"
	"github.com/koopa0/goen/internal/ui/pages"
)

const departmentOf = `
	WITH RECURSIVE up AS (
		SELECT c.id, c.parent_id, c.slug FROM categories c WHERE c.id = $1
		UNION ALL
		SELECT c.id, c.parent_id, c.slug FROM categories c JOIN up ON c.id = up.parent_id
	)
	SELECT slug FROM up WHERE parent_id IS NULL`

func TestRelatedProductsStayInTheDepartmentAndLeadWithTheirSubCategory(t *testing.T) {
	t.Parallel()
	rows, err := pool.Query(t.Context(), `SELECT slug, category_id FROM products WHERE status = 'active' ORDER BY slug`)
	if err != nil {
		t.Fatal(err)
	}
	categoryOf := map[string]uuid.UUID{}
	var slugs []string
	for rows.Next() {
		var slug string
		var category uuid.UUID
		if scanErr := rows.Scan(&slug, &category); scanErr != nil {
			t.Fatal(scanErr)
		}
		categoryOf[slug] = category
		slugs = append(slugs, slug)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}

	department := func(slug string) string {
		var root string
		if scanErr := pool.QueryRow(t.Context(), departmentOf, categoryOf[slug]).Scan(&root); scanErr != nil {
			t.Fatal(scanErr)
		}
		return root
	}

	store := product.NewStore(pool, slog.New(slog.DiscardHandler))
	for _, slug := range slugs {
		view, loadErr := store.Load(t.Context(), slug, product.Selection{})
		if loadErr != nil {
			t.Fatalf("load %s: %v", slug, loadErr)
		}
		if len(view.Related) > product.RelatedCount {
			t.Errorf("%s shows %d related products, want at most %d", slug, len(view.Related), product.RelatedCount)
		}
		prev := -1
		shown := map[string]bool{}
		fromOtherSubcategory := false
		for _, tile := range view.Related {
			shown[tile.Slug] = true
			fromOtherSubcategory = fromOtherSubcategory || categoryOf[tile.Slug] != categoryOf[slug]
			if tile.Slug == slug {
				t.Errorf("%s lists itself as related", slug)
			}
			if got, want := department(tile.Slug), department(slug); got != want {
				t.Errorf("%s (department %s) lists %s from department %s", slug, want, tile.Slug, got)
			}
			// Own sub-category first; within each group, what can be bought first.
			rank := 0
			if categoryOf[tile.Slug] != categoryOf[slug] {
				rank += 2
			}
			if !tile.InStock {
				rank++
			}
			if rank < prev {
				t.Errorf("%s lists %s (sub-category match %t, in stock %t) after a lower-ranked product", slug, tile.Slug, rank < 2, tile.InStock)
			}
			prev = rank
		}
		// A product from another sub-category may only appear once every
		// sibling has a place; sorting what is shown cannot see a missing one.
		if fromOtherSubcategory {
			for _, sibling := range slugs {
				if sibling != slug && categoryOf[sibling] == categoryOf[slug] && !shown[sibling] {
					t.Errorf("%s lists a product from another sub-category but leaves out its sibling %s", slug, sibling)
				}
			}
		}
	}
}

func TestAProductLoadsItsDepartmentsTone(t *testing.T) {
	t.Parallel()
	view, err := product.NewStore(pool, slog.New(slog.DiscardHandler)).Load(t.Context(), "nimbus-buds-pro", product.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if view.Tone != pages.ToneMist {
		t.Errorf("a product under tech/audio loads tone %q, want %q", view.Tone, pages.ToneMist)
	}
}
