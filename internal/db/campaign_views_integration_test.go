//go:build integration

package db_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/pgtx"
)

type campaignVariant struct {
	price, compare, stock, safety int // compare 0 is none
	inactive                      bool
}

// The views are read as store, the role of the pages that ask, so a missing grant
// fails here too.
func TestTheCampaignViewsHoldWhatIsRunningAndWhatIsADeal(t *testing.T) {
	const running = `now() - interval '1 day', now() + interval '1 day', true`
	deal := campaignVariant{price: 1000, compare: 2000, stock: 5}
	soldOutDeal := campaignVariant{price: 1000, compare: 2000}
	tests := []struct {
		name     string
		window   string // starts_at, ends_at, is_active
		draft    bool
		variants []campaignVariant
		running  bool
		deal     bool
	}{
		{name: "running, a discounted variant in stock", window: running,
			variants: []campaignVariant{deal}, running: true, deal: true},
		{name: "ended", window: `now() - interval '30 days', now() - interval '1 day', true`,
			variants: []campaignVariant{deal}},
		{name: "not started", window: `now() + interval '1 day', now() + interval '30 days', true`,
			variants: []campaignVariant{deal}},
		{name: "switched off", window: `now() - interval '1 day', now() + interval '1 day', false`,
			variants: []campaignVariant{deal}},
		{name: "only full-price stock: the discounted variant is sold out", window: running,
			variants: []campaignVariant{soldOutDeal, {price: 1000, stock: 5}}, running: true},
		{name: "only a sold-out discount", window: running,
			variants: []campaignVariant{soldOutDeal}, running: true},
		{name: "the discounted variant at its safety stock", window: running,
			variants: []campaignVariant{{price: 1000, compare: 2000, stock: 2, safety: 2}}, running: true},
		{name: "the discounted variant in stock is switched off", window: running,
			variants: []campaignVariant{{price: 1000, compare: 2000, stock: 5, inactive: true}, soldOutDeal}, running: true},
		{name: "the product is a draft", window: running, draft: true,
			variants: []campaignVariant{deal}, running: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			tx, err := schemaPool(t).Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer pgtx.Rollback(ctx, tx)

			token := "campaignview" + strings.ReplaceAll(uuid.NewString(), "-", "")
			var category, product, campaign uuid.UUID
			if err = tx.QueryRow(ctx, `INSERT INTO categories(slug,name,position) SELECT $1,'Campaign view',coalesce(max(position)+1,0) FROM categories WHERE parent_id IS NULL RETURNING id`, "cat-"+token).Scan(&category); err != nil {
				t.Fatalf("create category: %v", err)
			}
			if err = tx.QueryRow(ctx, `INSERT INTO products(category_id,slug,name,status) VALUES($1,$2,$3,'draft') RETURNING id`, category, token, "Campaign view "+token).Scan(&product); err != nil {
				t.Fatalf("create product: %v", err)
			}
			for i, v := range tt.variants {
				if _, err = tx.Exec(ctx, `
					INSERT INTO product_variants
					    (product_id, sku, price_cents, compare_at_price_cents, stock_quantity, safety_stock, is_active, position)
					VALUES ($1, $2, $3, NULLIF($4::bigint, 0), $5, $6, $7, $8)`,
					product, fmt.Sprintf("CV-%s-%d", strings.ToUpper(token[len(token)-8:]), i),
					v.price, v.compare, v.stock, v.safety, !v.inactive, i); err != nil {
					t.Fatalf("create variant %d: %v", i, err)
				}
			}
			if !tt.draft {
				if _, err = tx.Exec(ctx, `UPDATE products SET status='active', published_at=now() WHERE id=$1`, product); err != nil {
					t.Fatalf("publish product: %v", err)
				}
			}
			if err = tx.QueryRow(ctx, `INSERT INTO sale_campaigns(slug,title,starts_at,ends_at,is_active) VALUES($1,'Campaign view',`+tt.window+`) RETURNING id`, token).Scan(&campaign); err != nil {
				t.Fatalf("create campaign: %v", err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO sale_campaign_products(campaign_id,product_id) VALUES($1,$2)`, campaign, product); err != nil {
				t.Fatalf("feature product: %v", err)
			}

			if _, err = tx.Exec(ctx, `SET LOCAL ROLE store`); err != nil {
				t.Fatalf("set role store: %v", err)
			}
			var isRunning, isDeal, isListed bool
			if err = tx.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM running_campaigns WHERE id = $1),
				       EXISTS (SELECT 1 FROM campaign_deals WHERE campaign_id = $1 AND product_id = $2),
				       EXISTS (SELECT 1 FROM listed_campaigns WHERE id = $1)`,
				campaign, product).Scan(&isRunning, &isDeal, &isListed); err != nil {
				t.Fatalf("read the views as store: %v", err)
			}
			if isRunning != tt.running {
				t.Errorf("running_campaigns holds the campaign = %v, want %v", isRunning, tt.running)
			}
			if isDeal != tt.deal {
				t.Errorf("campaign_deals holds the product = %v, want %v", isDeal, tt.deal)
			}
			// The campaign features this product alone, so it is listed exactly when that is a deal.
			if isListed != tt.deal {
				t.Errorf("listed_campaigns holds the campaign = %v, want %v", isListed, tt.deal)
			}
		})
	}
}
