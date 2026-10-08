//go:build integration

package home_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgtx"
)

// The campaign's row prices a product as the campaign's page does: at its
// discounted variant when that can be bought, with "from" only while a cheaper
// variant that can be bought exists. The other rows keep the cheapest variant
// that can be bought.
func TestTheCampaignRowPricesTheDiscountedVariant(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer pgtx.Rollback(ctx, tx)
	if _, err = tx.Exec(ctx, `UPDATE sale_campaigns SET is_active = false`); err != nil {
		t.Fatalf("stop the seed's campaigns: %v", err)
	}

	type variant struct {
		price, compare     any
		stock, safetyStock int
	}
	tests := []struct {
		name              string
		variants          []variant
		wantPrice         int64
		wantCompare       int64
		wantFrom          bool
		wantNewest        int64
		wantNewestCompare int64
	}{
		{
			name:      "the discount is the dearer variant",
			variants:  []variant{{1000, nil, 5, 0}, {1500, 2000, 5, 0}, {3000, nil, 5, 0}},
			wantPrice: 1500, wantCompare: 2000, wantFrom: false,
			wantNewest: 1000,
		},
		{
			name:      "the discount is sold out beside full-price stock",
			variants:  []variant{{1000, nil, 5, 0}, {1500, 2000, 0, 0}, {3000, nil, 5, 0}},
			wantPrice: 1000, wantCompare: 0, wantFrom: true,
			wantNewest: 1000,
		},
		{
			name:      "the discount is at exactly its safety stock",
			variants:  []variant{{1000, nil, 5, 0}, {1500, 2000, 2, 2}, {3000, nil, 5, 0}},
			wantPrice: 1000, wantCompare: 0, wantFrom: true,
			wantNewest: 1000,
		},
		{
			name:      "a cheaper variant that cannot be bought does not hold back from",
			variants:  []variant{{1000, nil, 0, 0}, {1500, 2000, 5, 0}, {3000, nil, 5, 0}},
			wantPrice: 1500, wantCompare: 2000, wantFrom: true,
			wantNewest: 1500, wantNewestCompare: 2000,
		},
		{
			name:      "a dearer variant that cannot be bought still makes from",
			variants:  []variant{{1500, 2000, 5, 0}, {3000, nil, 0, 0}},
			wantPrice: 1500, wantCompare: 2000, wantFrom: true,
			wantNewest: 1500, wantNewestCompare: 2000,
		},
	}

	var campaign uuid.UUID
	campaignSlug := "campaign-row-" + uuid.NewString()
	if err = tx.QueryRow(ctx, `INSERT INTO sale_campaigns (slug, title, ends_at) VALUES ($1, '活動列', now() + interval '3 days') RETURNING id`, campaignSlug).Scan(&campaign); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	slugs := make([]string, len(tests))
	for i, tt := range tests {
		slugs[i] = "campaign-row-" + uuid.NewString()
		var product uuid.UUID
		if err = tx.QueryRow(ctx, `
			INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
			SELECT (SELECT id FROM brands LIMIT 1),
			       (SELECT id FROM categories WHERE parent_id IS NULL LIMIT 1),
			       $1, '活動列商品', 'active', now()
			RETURNING id`, slugs[i]).Scan(&product); err != nil {
			t.Fatalf("%s: create product: %v", tt.name, err)
		}
		for position, v := range tt.variants {
			if _, err = tx.Exec(ctx, `
				INSERT INTO product_variants (product_id, sku, price_cents, compare_at_price_cents, stock_quantity, safety_stock, position)
				VALUES ($1, 'ROW-' || upper(replace(gen_random_uuid()::text, '-', '')), $2, $3, $4, $5, $6)`,
				product, v.price, v.compare, v.stock, v.safetyStock, position); err != nil {
				t.Fatalf("%s: create variant: %v", tt.name, err)
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO sale_campaign_products (campaign_id, product_id, position) VALUES ($1, $2, $3)`, campaign, product, i); err != nil {
			t.Fatalf("%s: feature product: %v", tt.name, err)
		}
	}

	read := func(campaignID uuid.NullUUID) map[string]db.HomeTilesRow {
		rows, readErr := db.New(tx).HomeTiles(ctx, db.HomeTilesParams{
			Locale: string(i18n.ZhHant), CampaignID: campaignID, MaxTiles: 50,
		})
		if readErr != nil {
			t.Fatalf("read tiles: %v", readErr)
		}
		bySlug := make(map[string]db.HomeTilesRow, len(rows))
		for _, r := range rows {
			bySlug[r.Slug] = r
		}
		return bySlug
	}
	row := read(uuid.NullUUID{UUID: campaign, Valid: true})
	newest := read(uuid.NullUUID{})

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, ok := row[slugs[i]]
			if !ok {
				t.Fatalf("the campaign's row does not show its product")
			}
			if r.TilePriceCents != tt.wantPrice || r.CompareAtPriceCents.Int64 != tt.wantCompare || r.PriceVaries != tt.wantFrom {
				t.Errorf("campaign row tile = %d against %d, from %v; want %d against %d, from %v",
					r.TilePriceCents, r.CompareAtPriceCents.Int64, r.PriceVaries, tt.wantPrice, tt.wantCompare, tt.wantFrom)
			}
			n, ok := newest[slugs[i]]
			if !ok {
				t.Fatalf("the newest row does not show the product published last")
			}
			if n.TilePriceCents != tt.wantNewest || n.CompareAtPriceCents.Int64 != tt.wantNewestCompare {
				t.Errorf("newest row tile = %d against %d; want %d against %d",
					n.TilePriceCents, n.CompareAtPriceCents.Int64, tt.wantNewest, tt.wantNewestCompare)
			}
		})
	}
}
