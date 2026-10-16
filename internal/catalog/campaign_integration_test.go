//go:build integration

package catalog_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/catalog"
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
	store := catalog.NewStore(pool, slog.New(slog.DiscardHandler))

	view, err := store.Product(ctx, slug, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if view.Campaign.Running() || view.OnSale {
		t.Fatalf("a product in no campaign names %q, on sale %v", view.Campaign.Slug, view.OnSale)
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
	view, err = store.Product(ctx, slug, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if view.Campaign.Slug != campaign || view.Campaign.Title != "測試活動" || !view.OnSale {
		t.Errorf("a running campaign: got %q %q, on sale %v, want %q 測試活動 on sale", view.Campaign.Slug, view.Campaign.Title, view.OnSale, campaign)
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
		view, err = store.Product(ctx, slug, nil)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if view.Campaign.Running() || view.OnSale {
			t.Errorf("a campaign that is %s is still named: %q, on sale %v", tt.name, view.Campaign.Slug, view.OnSale)
		}
	}
}

// A campaign page judges its window in Go, and whether a card is struck follows
// the running_campaigns view in SQL. At both edges of the window they must agree,
// or a page would call a campaign running while its cards show full prices.
// Inside one transaction now() stands still, so a window can start or end
// exactly on it.
func TestCampaignStateAtAgreesWithRunningCampaigns(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(ctx)) })

	for _, tt := range []struct {
		name   string
		window string // starts_at, ends_at
		want   catalog.CampaignState
	}{
		{"starts now", `now(), now() + interval '1 day'`, catalog.CampaignRunning},
		{"starts a microsecond from now", `now() + interval '1 microsecond', now() + interval '1 day'`, catalog.CampaignNotStarted},
		{"ends a microsecond from now", `now() - interval '1 day', now() + interval '1 microsecond'`, catalog.CampaignRunning},
		{"ends now", `now() - interval '1 day', now()`, catalog.CampaignEnded},
	} {
		var (
			id                    uuid.UUID
			now, startsAt, endsAt time.Time
			running               bool
		)
		if err := tx.QueryRow(ctx, `
			INSERT INTO sale_campaigns (slug, title, starts_at, ends_at)
			VALUES ($1, '邊界', `+tt.window+`)
			RETURNING id, now(), starts_at, ends_at`, "edge-"+uuid.NewString()[:8]).Scan(&id, &now, &startsAt, &endsAt); err != nil {
			t.Fatalf("%s: create campaign: %v", tt.name, err)
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM running_campaigns WHERE id = $1)`, id).Scan(&running); err != nil {
			t.Fatalf("%s: read running_campaigns: %v", tt.name, err)
		}
		if want := tt.want == catalog.CampaignRunning; running != want {
			t.Fatalf("%s: running_campaigns holds the campaign = %v, want %v: the fixture misses the edge", tt.name, running, want)
		}
		if got := catalog.CampaignStateAt(startsAt, endsAt, now); got != tt.want {
			t.Errorf("%s: CampaignStateAt = %q, want %q, as running_campaigns judges it", tt.name, got, tt.want)
		}
	}
}
