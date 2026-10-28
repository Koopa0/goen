//go:build integration

package catalog_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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

// campaignDepartment runs a campaign that features one new product with the
// given stock, and returns the campaign's slug and the product's department.
func campaignDepartment(t *testing.T, tx pgx.Tx, stock int) (campaign, department string) {
	t.Helper()
	ctx := t.Context()
	campaign = "dept-" + uuid.NewString()[:8]
	if _, err := tx.Exec(ctx, `
		INSERT INTO sale_campaigns (slug, title, ends_at)
		VALUES ($1, '測試活動', now() + interval '1 minute')`, campaign); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	featureNewProduct(t, tx, campaign, stock, "active")
	if err := tx.QueryRow(ctx, `
		SELECT cat.slug FROM products p JOIN sale_campaign_products cp ON cp.product_id = p.id
		JOIN sale_campaigns c ON c.id = cp.campaign_id
		JOIN categories cat ON cat.id = p.category_id WHERE c.slug = $1`, campaign).Scan(&department); err != nil {
		t.Fatalf("department of the featured product: %v", err)
	}
	return campaign, department
}

// TestStore_DepartmentHeadCampaign holds that a department names a campaign
// only while it has a featured product to buy.
//
// The notice names a campaign only while the department has a featured product to buy:
// a sold-out one is not an offer, and a campaign that runs elsewhere is not this
// department's.
func TestStore_DepartmentHeadCampaign(t *testing.T) {
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
			s := catalog.NewStore(tx, slog.New(slog.DiscardHandler))
			slug, department := campaignDepartment(t, tx, tt.stock)

			head, err := s.DepartmentHead(ctx, catalog.ListingView{Slug: department, Theme: &catalog.Theme{}, Page: 1}, nil)
			if err != nil {
				t.Fatalf("DepartmentHead: %v", err)
			}
			if got := head.Notice != nil && head.Notice.Href == "/s/"+slug; got != tt.named {
				t.Errorf("notice names campaign = %v, want %v", got, tt.named)
			}
		})
	}
}

// TestStore_DepartmentHeadSmall holds that a comparable department of three
// products keeps its notice but carries no editorial.
//
// With three products the comparison or the colour story would repeat the grid
// below it.
func TestStore_DepartmentHeadSmall(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(ctx)) })
	s := catalog.NewStore(tx, slog.New(slog.DiscardHandler))
	slug, department := campaignDepartment(t, tx, 5)

	head, err := s.DepartmentHead(ctx, catalog.ListingView{
		Slug: department, Theme: &catalog.Theme{Comparable: true}, Page: 1, Total: 3,
	}, nil)
	if err != nil {
		t.Fatalf("DepartmentHead: %v", err)
	}
	if head.Notice == nil || head.Notice.Href != "/s/"+slug {
		t.Errorf("DepartmentHead notice = %v, want the campaign %q", head.Notice, slug)
	}
	if head.Preview != nil || head.Story != nil {
		t.Error("a department of three products carries an editorial")
	}
}

// TestStore_DepartmentHeadFiltered holds that the head of a filtered page is
// bare.
//
// A page of filtered results is not the department's front: no notice and no
// editorial.
func TestStore_DepartmentHeadFiltered(t *testing.T) {
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	head, err := s.DepartmentHead(t.Context(), catalog.ListingView{
		Slug: departmentOf(t, plainSlug(t)), Theme: &catalog.Theme{}, Page: 1, Filtered: true,
	}, nil)
	if err != nil {
		t.Fatalf("DepartmentHead: %v", err)
	}
	if head.Notice != nil || head.Preview != nil || head.Story != nil {
		t.Error("the head of a filtered page carries a notice or an editorial")
	}
}

// TestStore_ListingHighlights holds that a comparable shelf gives each tile its
// specification highlights.
//
// A department that compares lists its products with two specifications each.
func TestStore_ListingHighlights(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
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
