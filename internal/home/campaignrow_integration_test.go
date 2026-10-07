//go:build integration

package home_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/home"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgtx"
)

// The campaign's row prices a product as the campaign's page does: at its discounted
// variant, with no "from" while a cheaper variant exists. The other rows keep the
// cheapest variant that can be bought.
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

	slug := "campaign-row-" + uuid.NewString()
	var product, campaign uuid.UUID
	if err = tx.QueryRow(ctx, `
		WITH p AS (
		    INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		    SELECT (SELECT id FROM brands LIMIT 1),
		           (SELECT id FROM categories WHERE parent_id IS NULL LIMIT 1),
		           $1, '活動列商品', 'active', now()
		    RETURNING id
		), v AS (
		    INSERT INTO product_variants (product_id, sku, price_cents, compare_at_price_cents, stock_quantity, safety_stock, position)
		    SELECT p.id, 'ROW-' || upper(replace(gen_random_uuid()::text, '-', '')), x.price, x.compare, 5, 0, x.position
		    FROM p, (VALUES (1000, NULL::bigint, 0), (1500, 2000, 1), (3000, NULL, 2)) AS x(price, compare, position)
		)
		SELECT id FROM p`, slug).Scan(&product); err != nil {
		t.Fatalf("create product: %v", err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO sale_campaigns (slug, title, ends_at) VALUES ($1, '活動列', now() + interval '3 days') RETURNING id`, slug).Scan(&campaign); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO sale_campaign_products (campaign_id, product_id) VALUES ($1, $2)`, campaign, product); err != nil {
		t.Fatalf("feature product: %v", err)
	}

	view, err := home.NewStore(tx).Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if view.Row.Href != "/s/"+slug {
		t.Fatalf("the row is %q, want the campaign's", view.Row.Href)
	}
	found := false
	for _, tile := range view.Row.Tiles {
		if tile.Slug != slug {
			continue
		}
		found = true
		if tile.PriceCents != 1500 || tile.CompareCents != 2000 || tile.PriceVaries {
			t.Errorf("campaign row tile = %d against %d, from %v; want 1500 against 2000, not from",
				tile.PriceCents, tile.CompareCents, tile.PriceVaries)
		}
	}
	if !found {
		t.Fatalf("the campaign's row does not show its product")
	}

	rows, err := db.New(tx).HomeTiles(ctx, db.HomeTilesParams{Locale: string(i18n.ZhHant), MaxTiles: 50})
	if err != nil {
		t.Fatalf("read the newest row: %v", err)
	}
	found = false
	for _, r := range rows {
		if r.Slug != slug {
			continue
		}
		found = true
		if r.TilePriceCents != 1000 || r.CompareAtPriceCents.Valid || !r.PriceVaries {
			t.Errorf("newest row tile = %d against %v, from %v; want 1000 with no compare price, from",
				r.TilePriceCents, r.CompareAtPriceCents, r.PriceVaries)
		}
	}
	if !found {
		t.Fatalf("the newest row does not show the product published last")
	}
}
