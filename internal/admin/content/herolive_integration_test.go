//go:build integration

package content_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/content"
	"github.com/koopa0/goen/internal/ui/pages"
)

// The back office shows the carousel the storefront builds, so a campaign that
// feeds it appears there with its source named.
func TestTheHomeQueuePageCarriesTheCampaignSlidesTheStorefrontShows(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := content.NewStore(pool)
	// Queued slides come first in the carousel and there is room for three, so
	// what other tests left behind would push the campaign out.
	if _, err := pool.Exec(ctx, `DELETE FROM hero_slides`); err != nil {
		t.Fatalf("clear hero slides: %v", err)
	}
	title := "輪播活動 " + uuid.NewString()[:8]
	if _, err := pool.Exec(ctx, `
		INSERT INTO sale_campaigns (slug, title, ends_at)
		VALUES ($1, $2, now() + interval '1 hour')`, "live-"+uuid.NewString()[:8], title); err != nil {
		t.Fatalf("insert campaign: %v", err)
	}
	featureSellableProduct(t, ctx, title)
	view, err := s.HeroSlides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, slide := range view.Carousel {
		if slide.Title == title {
			if slide.Source != pages.SlideCampaign {
				t.Errorf("source = %q, want %q", slide.Source, pages.SlideCampaign)
			}
			return
		}
	}
	t.Errorf("the running campaign %q is not among the %d carousel slides", title, len(view.Carousel))
}

// A campaign takes a carousel slide only while it features a product someone can buy.
func featureSellableProduct(t *testing.T, ctx context.Context, campaignTitle string) {
	t.Helper()
	var productID uuid.UUID
	if err := pool.QueryRow(ctx, `
		WITH p AS (
		    INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		    SELECT (SELECT id FROM brands LIMIT 1),
		           (SELECT id FROM categories WHERE parent_id IS NULL LIMIT 1),
		           'hero-' || gen_random_uuid(), '活動商品', 'active', now()
		    RETURNING id
		), v AS (
		    INSERT INTO product_variants
		        (product_id, sku, price_cents, compare_at_price_cents, stock_quantity, safety_stock, position)
		    SELECT p.id, 'HERO-' || upper(replace(gen_random_uuid()::text, '-', '')), 1000, 2000, 5, 0, 0 FROM p
		)
		SELECT id FROM p`).Scan(&productID); err != nil {
		t.Fatalf("create product: %v", err)
	}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		for _, stmt := range []string{
			`DELETE FROM sale_campaign_products WHERE product_id = $1`,
			`DELETE FROM product_variants WHERE product_id = $1`,
			`DELETE FROM products WHERE id = $1`,
		} {
			if _, err := pool.Exec(clean, stmt, productID); err != nil {
				t.Errorf("remove product %s: %v", productID, err)
				return
			}
		}
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO sale_campaign_products (campaign_id, product_id)
		SELECT id, $2 FROM sale_campaigns WHERE title = $1`, campaignTitle, productID); err != nil {
		t.Fatalf("feature product: %v", err)
	}
}
