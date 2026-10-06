//go:build integration

package product_test

import (
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/product"
)

const departmentOf = `
	WITH RECURSIVE up AS (
		SELECT c.id, c.parent_id, c.slug FROM categories c WHERE c.id = $1
		UNION ALL
		SELECT c.id, c.parent_id, c.slug FROM categories c JOIN up ON c.id = up.parent_id
	)
	SELECT slug FROM up WHERE parent_id IS NULL`

func TestRelatedProductsStayInTheDepartmentAndLeadWithWhatCanBeBought(t *testing.T) {
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
		seenSoldOut, seenOtherSubcategory := false, false
		for _, tile := range view.Related {
			if tile.Slug == slug {
				t.Errorf("%s lists itself as related", slug)
			}
			if got, want := department(tile.Slug), department(slug); got != want {
				t.Errorf("%s (department %s) lists %s from department %s", slug, want, tile.Slug, got)
			}
			if tile.InStock && seenSoldOut {
				t.Errorf("%s lists the sellable %s after a sold-out product", slug, tile.Slug)
			}
			sameSubcategory := categoryOf[tile.Slug] == categoryOf[slug]
			if tile.InStock && sameSubcategory && seenOtherSubcategory {
				t.Errorf("%s lists %s from its own sub-category after one from another", slug, tile.Slug)
			}
			seenSoldOut = seenSoldOut || !tile.InStock
			seenOtherSubcategory = seenOtherSubcategory || (tile.InStock && !sameSubcategory)
		}
	}
}
