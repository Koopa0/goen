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

	"github.com/koopa0/goen/internal/catalog/queryplan"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/db/dbtest"
)

//go:embed testdata/home_before_pagination.sql
var homeBeforePagination string

func TestHomeRankingEnrichesOnlySelectedProducts(t *testing.T) {
	pool := dbtest.Pool(t)
	if err := queryplan.LoadSeed(t.Context(), pool, queryplan.ScaleLarge); err != nil {
		t.Fatal(err)
	}
	query := queryplan.Query{Route: queryplan.RouteHomeRecommended,
		SQL: db.HarnessCatalogueSQL.HomeRecommendedTiles, Args: []any{int32(8), "en"}}
	result, err := queryplan.Measure(t.Context(), pool, query, queryplan.ScaleLarge, false, 0, "home-plan-test")
	if err != nil {
		t.Fatal(err)
	}
	assertHomePageEnrichment(t, result.PlanJSON)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	// Unique publication times make the original two-key ordering deterministic.
	// High-rated products without active variants must be excluded before LIMIT.
	const fixtures = `
WITH ordering AS (
    SELECT id, row_number() OVER (ORDER BY id) AS position FROM products
) UPDATE products p SET published_at = now() - ordering.position * interval '1 second'
FROM ordering WHERE p.id = ordering.id;
INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
SELECT brand_id, category_id, 'home-orphan', 'Home orphan', 'active', now()
FROM products WHERE slug = 'scale-00001';
INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
SELECT brand_id, category_id, 'home-inactive', 'Home inactive', 'active', now() + interval '1 second'
FROM products WHERE slug = 'scale-00001';
INSERT INTO product_variants (product_id, sku, price_cents, is_active)
SELECT id, 'HOME-INACTIVE', 100, false FROM products WHERE slug = 'home-inactive';
INSERT INTO product_reviews (product_id, rating, title, body)
SELECT p.id, 5, 'Fixture rating', 'Fixture review'
FROM products p CROSS JOIN generate_series(1, 20)
WHERE p.slug IN ('home-orphan', 'home-inactive');`
	if _, fixtureErr := tx.Exec(t.Context(), fixtures); fixtureErr != nil {
		t.Fatal(fixtureErr)
	}
	queries := db.New(tx)
	for _, locale := range []string{"en", "zh-TW"} {
		for _, limit := range []int32{0, 1, 8, 24} {
			t.Run(fmt.Sprintf("%s/%d", locale, limit), func(t *testing.T) {
				assertSameHomePage(t, tx, queries, db.HomeRecommendedTilesParams{Limit: limit, Locale: locale})
			})
		}
	}
	params := db.HomeRecommendedTilesParams{Limit: 8, Locale: "en"}
	before, err := queries.HomeRecommendedTiles(t.Context(), params)
	if err != nil || len(before) != 8 {
		t.Fatalf("initial home page rows=%d error=%v", len(before), err)
	}
	if _, changeErr := tx.Exec(t.Context(), `
UPDATE product_variants SET price_cents = price_cents + 111
WHERE product_id = (SELECT id FROM products WHERE slug = $1);
`, before[0].Slug); changeErr != nil {
		t.Fatal(changeErr)
	}
	if _, changeErr := tx.Exec(t.Context(), `SELECT record_inventory_movement(v.id, -v.stock_quantity,
 'adjustment', 'home-page-live:' || v.id::text)
 FROM product_variants v JOIN products p ON p.id = v.product_id
 WHERE p.slug = $1 AND v.stock_quantity > 0`, before[0].Slug); changeErr != nil {
		t.Fatal(changeErr)
	}
	after, err := queries.HomeRecommendedTiles(t.Context(), params)
	if err != nil || len(after) != 8 || after[0].Slug != before[0].Slug || after[0].InStock || after[0].MinPriceCents == before[0].MinPriceCents {
		t.Fatalf("home page did not observe live price/stock: before=%+v after=%+v error=%v", before, after, err)
	}
	assertSameHomePage(t, tx, queries, params)
	if _, hideErr := tx.Exec(t.Context(), `UPDATE product_reviews SET hidden_at = now()`); hideErr != nil {
		t.Fatal(hideErr)
	}
	assertSameHomePage(t, tx, queries, params)
}

func assertSameHomePage(t *testing.T, tx pgx.Tx, queries *db.Queries, params db.HomeRecommendedTilesParams) {
	t.Helper()
	rows, err := tx.Query(t.Context(), homeBeforePagination, params.Limit, params.Locale)
	if err != nil {
		t.Fatal(err)
	}
	want, err := pgx.CollectRows(rows, pgx.RowToStructByPos[db.HomeRecommendedTilesRow])
	if err != nil {
		t.Fatal(err)
	}
	got, err := queries.HomeRecommendedTiles(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("home ranking or card presentation changed:\ngot %+v\nwant %+v", got, want)
	}
}

func assertHomePageEnrichment(t *testing.T, payload json.RawMessage) {
	t.Helper()
	var roots []struct{ Plan searchPlanNode }
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
	if loops["product_images"] < 1 || loops["product_images"] > 8 {
		t.Fatalf("image enrichment loops=%d for eight cards", loops["product_images"])
	}
	// Grouped/global scans may have parallel workers, but cannot run per product.
	if loops["product_reviews"] < 1 || loops["product_reviews"] > 8 {
		t.Fatalf("review scans=%d, want bounded global and grouped scans", loops["product_reviews"])
	}
}
