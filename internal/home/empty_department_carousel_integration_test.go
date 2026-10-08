//go:build integration

package home_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/home"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestCarouselDepartmentsNeedListedProducts(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer pgtx.Rollback(ctx, tx)

			// The carousel must reach these roots before its three-slide limit;
			// all catalogue changes remain private to this rollback transaction.
			for _, q := range []string{
				`DELETE FROM hero_slides`,
				`UPDATE sale_campaigns SET is_active = false`,
				`UPDATE categories SET image_key = NULL`,
			} {
				if _, err = tx.Exec(ctx, q); err != nil {
					t.Fatalf("isolate carousel: %v", err)
				}
			}

			var subtree uuid.UUID
			for _, root := range []struct {
				slug   string
				status string
				photo  bool
				child  bool
			}{
				{"carousel-empty", "", true, false},
				{"carousel-draft", "draft", true, false},
				{"carousel-archived", "archived", true, false},
				{"carousel-no-photo", "active", false, false},
				{"carousel-subtree", "active", true, true},
				{"carousel-listed", "active", true, false},
				{"carousel-sold-out", "active", true, false},
				{"carousel-fourth", "active", true, false},
			} {
				var image any
				if root.photo {
					image = "department-tech.webp"
				}
				var categoryID uuid.UUID
				if err = tx.QueryRow(ctx, `
					INSERT INTO categories (slug, name, image_key, image_alt, position)
					VALUES ($1, $1, $2, 'Department photograph',
					    (SELECT coalesce(max(position), -1) + 1 FROM categories WHERE parent_id IS NULL))
					RETURNING id`, root.slug, image).Scan(&categoryID); err != nil {
					t.Fatalf("insert %s: %v", root.slug, err)
				}
				if root.child {
					subtree = categoryID
					if err = tx.QueryRow(ctx, `
						INSERT INTO categories (slug, name, parent_id, position)
						VALUES ('carousel-child', 'Carousel child', $1, 0)
						RETURNING id`, categoryID).Scan(&categoryID); err != nil {
						t.Fatalf("insert child: %v", err)
					}
				}
				if root.status == "" {
					continue
				}
				stock := 5
				if root.slug == "carousel-sold-out" {
					stock = 0
				}
				if _, err = tx.Exec(ctx, `
					WITH p AS (
					    INSERT INTO products (category_id, slug, name, status, published_at)
					    VALUES ($1, $2, $2, $3, now()) RETURNING id
					)
					INSERT INTO product_variants (product_id, sku, price_cents, stock_quantity, safety_stock, position)
					SELECT p.id, upper($2), 1000, $4, 0, 0 FROM p`,
					categoryID, root.slug, root.status, stock); err != nil {
					t.Fatalf("insert %s product: %v", root.slug, err)
				}
			}
			if _, err = tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
				t.Fatalf("validate catalogue fixture: %v", err)
			}

			store := home.NewStore(tx)
			slides, err := store.Carousel(ctx)
			if err != nil {
				t.Fatalf("Carousel(): %v", err)
			}
			hrefs := make([]string, 0, len(slides))
			for _, slide := range slides {
				switch slide.CTA.Href {
				case "/c/carousel-empty", "/c/carousel-draft", "/c/carousel-archived":
					t.Errorf("Carousel() features %q without a listed product", slide.CTA.Href)
				}
				if slide.Source != pages.SlideDepartment || !slide.Photo.Shown() {
					t.Errorf("Carousel() slide = %+v, want a photographed department", slide)
				}
				hrefs = append(hrefs, slide.CTA.Href)
			}
			want := []string{"/c/carousel-subtree", "/c/carousel-listed", "/c/carousel-sold-out"}
			if diff := cmp.Diff(want, hrefs); diff != "" {
				t.Errorf("Carousel() department links mismatch (-want +got):\n%s", diff)
			}

			if _, err = tx.Exec(ctx, `
				WITH RECURSIVE tree AS (
				    SELECT id FROM categories WHERE id = $1
				    UNION ALL
				    SELECT c.id FROM categories c JOIN tree t ON c.parent_id = t.id
				)
				UPDATE products SET status = 'draft' WHERE category_id IN (SELECT id FROM tree)`, subtree); err != nil {
				t.Fatalf("unlist subtree: %v", err)
			}
			slides, err = store.Carousel(ctx)
			if err != nil {
				t.Fatalf("Carousel() after unlisting: %v", err)
			}
			hrefs = hrefs[:0]
			for _, slide := range slides {
				hrefs = append(hrefs, slide.CTA.Href)
			}
			want = []string{"/c/carousel-listed", "/c/carousel-sold-out", "/c/carousel-fourth"}
			if diff := cmp.Diff(want, hrefs); diff != "" {
				t.Errorf("Carousel() after unlisting mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
