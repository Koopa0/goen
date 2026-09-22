//go:build integration

package queryplan_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/db"
)

//go:embed testdata/search_before_pagination.sql
var searchBeforePagination string

type searchPlanNode struct {
	Relation string           `json:"Relation Name"`
	Loops    int              `json:"Actual Loops"`
	Plans    []searchPlanNode `json:"Plans"`
}

func assertSearchPageEnrichment(t *testing.T, payload json.RawMessage) {
	t.Helper()
	var roots []struct {
		Plan searchPlanNode `json:"Plan"`
	}
	if err := json.Unmarshal(payload, &roots); err != nil {
		t.Fatal(err)
	}
	loops := map[string]int{}
	var visit func(searchPlanNode)
	visit = func(node searchPlanNode) {
		loops[node.Relation] += node.Loops
		for _, child := range node.Plans {
			visit(child)
		}
	}
	for _, root := range roots {
		visit(root.Plan)
	}
	for _, relation := range []string{"product_images", "product_reviews"} {
		if loops[relation] < 1 || loops[relation] > catalog.PageSize {
			t.Errorf("%s enriched %d products for a %d-product page", relation, loops[relation], catalog.PageSize)
		}
	}
}

func assertSearchPageSemantics(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()
	tx, beginErr := pool.Begin(ctx)
	if beginErr != nil {
		t.Fatal(beginErr)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// Deferred publication constraints let the oracle check variant eligibility
	// before pagination, even during a catalogue-edit transaction.
	const fixtures = `
INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
SELECT brand_id, category_id, 'page-orphan', 'Meridian orphan', 'active', now()
FROM products WHERE slug = 'scale-00001';
WITH inactive AS (
    INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
    SELECT brand_id, category_id, 'page-inactive', 'Meridian inactive', 'active', now()
    FROM products WHERE slug = 'scale-00001'
    RETURNING id
)
INSERT INTO product_variants (product_id, sku, price_cents, is_active)
SELECT id, 'PAGE-INACTIVE', 100, false FROM inactive;
INSERT INTO product_specs (product_id, label, value_en, value)
SELECT id, 'page boundary', 'needle-spec', 'fixture' FROM products WHERE slug = 'scale-00001';
UPDATE products SET name = 'needle-order', name_en = 'localized needle-order',
    summary = 'first fixture', summary_en = NULL, published_at = now() - interval '1 day'
WHERE slug = 'scale-00001';
UPDATE products SET summary_en = 'needle-order', published_at = now()
WHERE slug = 'scale-00002';`
	if _, fixtureErr := tx.Exec(ctx, fixtures); fixtureErr != nil {
		t.Fatal(fixtureErr)
	}
	queries := db.New(tx)
	for _, locale := range []string{"en", "zh-TW"} {
		for _, pattern := range []string{"%Meridian%", "%Scale%", "%\u91cf\u6e2c%", "%needle-spec%", "%needle-order%", "%zzznomatchzz%"} {
			for _, offset := range []int32{0, 24, 1992, 2016} {
				t.Run(fmt.Sprintf("%s/%s/%d", locale, pattern, offset), func(t *testing.T) {
					params := db.SearchProductsParams{Locale: locale, Pattern: pattern, PageOffset: offset, PageSize: catalog.PageSize}
					assertSameSearchPage(t, tx, queries, params)
				})
			}
		}
	}
	params := db.SearchProductsParams{Locale: "en", Pattern: "%needle-spec%", PageSize: catalog.PageSize}
	before, beforeErr := queries.SearchProducts(ctx, params)
	if beforeErr != nil || len(before) != 1 || !before[0].InStock {
		t.Fatalf("live-data fixture must have one stocked result: rows=%+v err=%v", before, beforeErr)
	}
	if _, changeErr := tx.Exec(ctx, `
UPDATE product_variants SET price_cents = price_cents + 111
WHERE product_id = (SELECT id FROM products WHERE slug = 'scale-00001');
SELECT record_inventory_movement(v.id, -v.stock_quantity, 'adjustment', 'search-page-live-stock')
FROM product_variants v JOIN products p ON p.id = v.product_id WHERE p.slug = 'scale-00001';`); changeErr != nil {
		t.Fatal(changeErr)
	}
	after, afterErr := queries.SearchProducts(ctx, params)
	if afterErr != nil || len(after) != 1 || after[0].InStock || after[0].MinPriceCents != before[0].MinPriceCents+111 {
		t.Fatalf("page must observe current price and stock: before=%+v after=%+v err=%v", before, after, afterErr)
	}
	assertSameSearchPage(t, tx, queries, params)
}

func assertSameSearchPage(t *testing.T, tx pgx.Tx, queries *db.Queries, params db.SearchProductsParams) {
	t.Helper()
	rows, queryErr := tx.Query(t.Context(), searchBeforePagination,
		params.Locale, params.Pattern, params.PageOffset, params.PageSize)
	if queryErr != nil {
		t.Fatal(queryErr)
	}
	want, collectErr := pgx.CollectRows(rows, pgx.RowToStructByPos[db.SearchProductsRow])
	if collectErr != nil {
		t.Fatal(collectErr)
	}
	got, readErr := queries.SearchProducts(t.Context(), params)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("search page changed identity/order/presentation:\ngot  %+v\nwant %+v", got, want)
	}
}
