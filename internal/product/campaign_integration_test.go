//go:build integration

package product_test

import (
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/product"
)

func TestAProductPageNamesTheCampaignOnlyWhileItRuns(t *testing.T) {
	ctx := t.Context()
	slug := "campaign-" + uuid.NewString()
	if _, err := pool.Exec(ctx, `
		WITH p AS (
		    INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		    SELECT (SELECT id FROM brands LIMIT 1),
		           (SELECT id FROM categories WHERE parent_id IS NULL LIMIT 1),
		           $1, '活動商品', 'active', now()
		    RETURNING id
		)
		INSERT INTO product_variants
		    (product_id, sku, price_cents, compare_at_price_cents, stock_quantity, safety_stock, position)
		SELECT p.id, upper(replace($1, '-', '')), 1000, 2000, 5, 0, 0 FROM p`, slug); err != nil {
		t.Fatalf("create product: %v", err)
	}
	store := product.NewStore(pool, slog.New(slog.DiscardHandler))

	view, err := store.Load(ctx, slug, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if view.Campaign.Running() {
		t.Fatalf("a product in no campaign names %q", view.Campaign.Slug)
	}

	campaign := "page-" + uuid.NewString()[:8]
	if _, err = pool.Exec(ctx, `INSERT INTO sale_campaigns (slug, title, ends_at) VALUES ($1, '測試活動', now() + interval '7 days')`, campaign); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO sale_campaign_products (campaign_id, product_id)
		SELECT c.id, p.id FROM sale_campaigns c, products p WHERE c.slug = $1 AND p.slug = $2`, campaign, slug); err != nil {
		t.Fatalf("feature: %v", err)
	}
	view, err = store.Load(ctx, slug, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if view.Campaign.Slug != campaign || view.Campaign.Title != "測試活動" {
		t.Errorf("a running campaign: got %q %q, want %q 測試活動", view.Campaign.Slug, view.Campaign.Title, campaign)
	}

	for _, tt := range []struct{ name, setup string }{
		{"ended", `UPDATE sale_campaigns SET starts_at = now() - interval '30 days', ends_at = now() - interval '1 day' WHERE slug = $1`},
		{"not started", `UPDATE sale_campaigns SET starts_at = now() + interval '1 day', ends_at = now() + interval '30 days' WHERE slug = $1`},
		{"switched off", `UPDATE sale_campaigns SET is_active = false WHERE slug = $1`},
		{"nothing left to buy", `UPDATE product_variants SET stock_quantity = 0
			WHERE product_id IN (SELECT cp.product_id FROM sale_campaign_products cp
			                     JOIN sale_campaigns c ON c.id = cp.campaign_id WHERE c.slug = $1)`},
	} {
		if _, err = pool.Exec(ctx, `UPDATE product_variants SET stock_quantity = 5 WHERE product_id = (SELECT id FROM products WHERE slug = $1)`, slug); err != nil {
			t.Fatalf("restock: %v", err)
		}
		if _, err = pool.Exec(ctx, `UPDATE sale_campaigns SET starts_at = now() - interval '1 day', ends_at = now() + interval '7 days', is_active = true WHERE slug = $1`, campaign); err != nil {
			t.Fatalf("reset: %v", err)
		}
		if _, err = pool.Exec(ctx, tt.setup, campaign); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		view, err = store.Load(ctx, slug, nil)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if view.Campaign.Running() {
			t.Errorf("a campaign that is %s is still named: %q", tt.name, view.Campaign.Slug)
		}
	}
}
