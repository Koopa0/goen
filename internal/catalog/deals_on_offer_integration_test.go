//go:build integration

package catalog_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/catalog"
)

func TestDealsOnOfferNeedsAProductOrACampaignThatCanBeBought(t *testing.T) {
	tests := []struct {
		name string
		// price, compare and stock describe the product's one variant (compare 0 is none); campaign
		// features it on a running campaign, which needs a compare-at price even if it shows no saving.
		price, compare, stock int
		campaign              bool
		want                  bool
	}{
		{name: "discounted but out of stock, on a campaign", price: 1000, compare: 2000, stock: 0, campaign: true, want: false},
		{name: "a discounted product in stock", price: 1000, compare: 2000, stock: 5, want: true},
		{name: "a campaign whose product shows no saving", price: 1000, compare: 1000, stock: 5, campaign: true, want: true},
		{name: "a product that is not discounted, on no campaign", price: 1000, compare: 0, stock: 5, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(ctx)) })
			// The seed's own deals must not decide the answer.
			for _, q := range []string{
				`DELETE FROM sale_campaign_products`,
				`UPDATE product_variants SET compare_at_price_cents = NULL`,
				`UPDATE sale_campaigns SET is_active = false`,
			} {
				if _, err := tx.Exec(ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			slug := "deals-" + uuid.NewString()
			if _, err := tx.Exec(ctx, `
				WITH p AS (
				    INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
				    SELECT (SELECT id FROM brands LIMIT 1),
				           (SELECT id FROM categories WHERE parent_id IS NULL LIMIT 1),
				           $1, '商品', 'active', now()
				    RETURNING id
				)
				INSERT INTO product_variants
				    (product_id, sku, price_cents, compare_at_price_cents, stock_quantity, safety_stock, position)
				SELECT p.id, upper(replace($1, '-', '')), $2, NULLIF($3, 0), $4, 0, 0 FROM p`,
				slug, tt.price, tt.compare, tt.stock); err != nil {
				t.Fatalf("create product: %v", err)
			}
			if tt.campaign {
				if _, err := tx.Exec(ctx, `
					WITH c AS (
					    INSERT INTO sale_campaigns (slug, title, starts_at, ends_at, is_active)
					    VALUES ($1, '活動', now() - interval '1 day', now() + interval '1 day', true)
					    RETURNING id
					)
					INSERT INTO sale_campaign_products (campaign_id, product_id)
					SELECT c.id, p.id FROM c, products p WHERE p.slug = $1`, slug); err != nil {
					t.Fatalf("create campaign: %v", err)
				}
			}
			got, err := catalog.NewStore(tx).DealsOnOffer(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("DealsOnOffer = %v, want %v", got, tt.want)
			}
		})
	}
}
