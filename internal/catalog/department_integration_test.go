//go:build integration

package catalog_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/catalog"
)

// departmentOf is the slug of the category a product sits in.
func departmentOf(t *testing.T, productSlug string) string {
	t.Helper()
	var slug string
	if err := pool.QueryRow(t.Context(), `
		SELECT c.slug FROM products p JOIN categories c ON c.id = p.category_id WHERE p.slug = $1`,
		productSlug).Scan(&slug); err != nil {
		t.Fatalf("category of %s: %v", productSlug, err)
	}
	return slug
}

// The notice names a campaign only while the department has a featured product to buy:
// a sold-out one is not an offer, and a campaign that runs elsewhere is not this
// department's.
func TestADepartmentNamesACampaignOnlyWhileItHasAFeaturedProductToBuy(t *testing.T) {
	ctx := t.Context()
	for _, tt := range []struct {
		name  string
		stock int
		named bool
	}{
		{"sold out", 0, false},
		{"in stock", 5, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Rolled back: a committed campaign would be listed for every test that
			// reads the deals page after this one.
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(ctx)) })
			s := catalog.NewStore(tx)
			slug := "dept-" + uuid.NewString()[:8]
			if _, err = tx.Exec(ctx, `
				INSERT INTO sale_campaigns (slug, title, ends_at)
				VALUES ($1, '測試活動', now() + interval '1 minute')`, slug); err != nil {
				t.Fatalf("create campaign: %v", err)
			}
			featureNewProduct(t, tx, slug, tt.stock, "active")
			var department string
			if err = tx.QueryRow(ctx, `
				SELECT cat.slug FROM products p JOIN sale_campaign_products cp ON cp.product_id = p.id
				JOIN sale_campaigns c ON c.id = cp.campaign_id
				JOIN categories cat ON cat.id = p.category_id WHERE c.slug = $1`, slug).Scan(&department); err != nil {
				t.Fatalf("department of the featured product: %v", err)
			}

			head, err := s.DepartmentHead(ctx, department, false, true, nil)
			if err != nil {
				t.Fatalf("DepartmentHead: %v", err)
			}
			if got := head.Notice != nil && head.Notice.Href == "/s/"+slug; got != tt.named {
				t.Errorf("notice names campaign = %v, want %v", got, tt.named)
			}
		})
	}
}

// A head read without slots carries no notice and no editorial.
func TestADepartmentHeadWithoutSlotsIsBare(t *testing.T) {
	s := catalog.NewStore(pool)
	head, err := s.DepartmentHead(t.Context(), departmentOf(t, plainSlug(t)), false, false, nil)
	if err != nil {
		t.Fatalf("DepartmentHead: %v", err)
	}
	if head.Notice != nil || head.Preview != nil || head.Story != nil {
		t.Error("a head read without slots carries a notice or an editorial")
	}
}

// A department that compares lists its products with two specifications each.
func TestAComparableShelfGivesEachTileItsSpecHighlights(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool)
	slug, _ := twoProductsWithSpecs(t)
	dept := departmentOf(t, slug)
	if _, err := pool.Exec(ctx, `UPDATE categories SET comparable = true WHERE slug = $1`, dept); err != nil {
		t.Fatalf("make the department compare: %v", err)
	}
	view, err := s.Listing(ctx, dept, catalog.Filters{})
	if err != nil {
		t.Fatalf("Listing: %v", err)
	}
	if len(view.Products) != 1 || !view.Products[0].Comparable {
		t.Fatalf("shelf holds %d products, want the one comparable product", len(view.Products))
	}
	if n := len(view.Products[0].Highlights); n != 2 {
		t.Errorf("tile has %d highlights, want its 2 specifications", n)
	}
}
