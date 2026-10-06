//go:build integration

package catalog_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/catalog"
)

type dealVariant struct {
	price, compare, stock int // compare 0 is none
}

func TestDealsOnOfferNeedsARunningCampaignWithSomethingToBuy(t *testing.T) {
	discounted := dealVariant{price: 1000, compare: 2000, stock: 5}
	soldOut := dealVariant{price: 1000, compare: 2000, stock: 0}
	plain := dealVariant{price: 1000, stock: 5}
	tests := []struct {
		name     string
		variants []dealVariant
		campaign bool // features the product on a running campaign
		want     bool
	}{
		{name: "neither: the discounted variant is sold out", variants: []dealVariant{soldOut}, campaign: true, want: false},
		{name: "a discounted product in stock, on no campaign", variants: []dealVariant{discounted}, want: false},
		{name: "a campaign with a discounted product in stock", variants: []dealVariant{discounted}, campaign: true, want: true},
		{name: "a campaign alone: its discounted variant is sold out, another is in stock", variants: []dealVariant{soldOut, plain}, campaign: true, want: true},
		{name: "a product that is not discounted, on no campaign", variants: []dealVariant{plain}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(ctx)) })
			// The seed's own deals must not decide the answer. Campaigns go first: a
			// featured product may not lose its last discount.
			for _, q := range []string{
				`DELETE FROM sale_campaign_products`,
				`UPDATE product_variants SET compare_at_price_cents = NULL`,
				`UPDATE sale_campaigns SET is_active = false`,
			} {
				if _, err = tx.Exec(ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			slug := "deals-" + uuid.NewString()
			if _, err = tx.Exec(ctx, `
				INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
				SELECT (SELECT id FROM brands LIMIT 1),
				       (SELECT id FROM categories WHERE parent_id IS NULL LIMIT 1),
				       $1, '商品', 'draft', NULL`, slug); err != nil {
				t.Fatalf("create product: %v", err)
			}
			for i, v := range tt.variants {
				if _, err = tx.Exec(ctx, `
					INSERT INTO product_variants
					    (product_id, sku, price_cents, compare_at_price_cents, stock_quantity, safety_stock, position)
					SELECT id, upper(replace($1::text, '-', '')) || $2::int::text, $3::bigint, NULLIF($4::bigint, 0), $5::int, 0, $2::int
					FROM products WHERE slug = $1`, slug, i, v.price, v.compare, v.stock); err != nil {
					t.Fatalf("create variant: %v", err)
				}
			}
			if _, err = tx.Exec(ctx, `UPDATE products SET status = 'active', published_at = now() WHERE slug = $1`, slug); err != nil {
				t.Fatalf("publish product: %v", err)
			}
			if tt.campaign {
				if _, err = tx.Exec(ctx, `
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
