//go:build integration

package queryplan_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/catalog/queryplan"
	"github.com/koopa0/goen/internal/db"
)

//go:embed testdata/category_before_pagination.sql
var categoryBeforePagination string

//go:embed testdata/category_count_before_pagination.sql
var categoryCountBeforePagination string

func assertCategoryProductionPages(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	q := db.New(pool)
	category, err := q.CategoryBySlug(t.Context(), db.CategoryBySlugParams{Slug: "phones", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := q.CategoryDescendants(t.Context(), category.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range queryplan.Queries(queryplan.ScaleLarge, ids, category.ID) {
		if query.CountRead {
			continue
		}
		switch query.Route {
		case queryplan.RouteCategoryListing, queryplan.RouteCategoryFiltered, queryplan.RouteCategoryPriceAsc, queryplan.RouteCategoryDeepPage:
			t.Run(string(query.Route)+" enrichment", func(t *testing.T) {
				if query.SQL != db.HarnessCatalogueSQL.CategoryListing {
					t.Fatal("category harness diverged from production SQL")
				}
				result, err := queryplan.Measure(t.Context(), pool, query, queryplan.ScaleLarge, false, 0, "category-page-shape")
				if err != nil {
					t.Fatal(err)
				}
				assertCategoryPageEnrichment(t, result.PlanJSON, result.ActualRows)
			})
		default:
			continue
		}
	}
	t.Run("category page semantics", func(t *testing.T) { assertCategoryPageSemantics(t, pool, ids) })
}

func assertCategoryPageEnrichment(t *testing.T, payload json.RawMessage, pageRows int64) {
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
		actual, exists := loops[relation]
		if !exists || int64(actual) > pageRows || (pageRows > 0 && actual == 0) {
			t.Errorf("%s enrichment loops=%d for %d returned products", relation, actual, pageRows)
		}
	}
}

func assertCategoryPageSemantics(t *testing.T, pool *pgxpool.Pool, ids []uuid.UUID) {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	const fixtures = `
INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
SELECT brand_id, category_id, 'category-page-orphan', 'Orphan', 'active', now()
FROM products WHERE slug = 'scale-00001';
WITH inactive AS (
 INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
 SELECT brand_id, category_id, 'category-page-inactive', 'Inactive', 'active', now()
 FROM products WHERE slug = 'scale-00001' RETURNING id
)
INSERT INTO product_variants (product_id,sku,price_cents,is_active)
SELECT id,'CATEGORY-PAGE-INACTIVE',100,false FROM inactive;
UPDATE products SET published_at=now(),name_en='Localized category fixture',summary_en=NULL
WHERE slug IN ('scale-00001','scale-00002');
UPDATE product_variants SET price_cents=150,compare_at_price_cents=NULL WHERE sku='SC-00001-0';
UPDATE product_variants SET price_cents=100,compare_at_price_cents=NULL WHERE sku='SC-00002-0';
UPDATE product_variants SET price_cents=200,compare_at_price_cents=NULL WHERE sku='SC-00002-1';`
	if _, err := tx.Exec(t.Context(), fixtures); err != nil {
		t.Fatal(err)
	}
	var brand uuid.UUID
	if err := tx.QueryRow(t.Context(), `SELECT brand_id FROM products WHERE slug='scale-00001'`).Scan(&brand); err != nil {
		t.Fatal(err)
	}
	queries := db.New(tx)
	for _, locale := range []string{"en", "zh-Hant"} {
		for _, sort := range []string{"", "price_asc", "price_desc", "rating"} {
			for _, offset := range []int32{0, 24, 4800} {
				for _, filter := range categoryPageFilters(brand) {
					params := db.CategoryListingParams{Locale: locale, Sort: sort, CategoryIds: ids, BrandIds: filter.brands, FilterVariants: filter.variants, InStockOnly: filter.stock, MinPrice: filter.min, MaxPrice: filter.max, PageOffset: offset, PageSize: catalog.PageSize}
					t.Run(fmt.Sprintf("%s/%s/%d/%s", locale, sort, offset, filter.name), func(t *testing.T) { assertSameCategoryPage(t, tx, queries, params) })
				}
			}
		}
	}
	assertCategoryLivePrices(t, tx, queries, ids)
	params := db.CategoryListingParams{Locale: "en", CategoryIds: []uuid.UUID{}, BrandIds: []uuid.UUID{}, PageSize: catalog.PageSize}
	assertSameCategoryPage(t, tx, queries, params)
}

type categoryPageFilter struct {
	name            string
	brands          []uuid.UUID
	variants, stock bool
	min, max        int64
}

func categoryPageFilters(brand uuid.UUID) []categoryPageFilter {
	return []categoryPageFilter{
		{name: "unfiltered", brands: []uuid.UUID{}},
		{name: "stock minimum", brands: []uuid.UUID{}, variants: true, stock: true, min: 100000},
		{name: "price range", brands: []uuid.UUID{}, variants: true, min: 100, max: 175},
		{name: "same variant stock and price", brands: []uuid.UUID{}, variants: true, stock: true, min: 100, max: 175},
		{name: "brand", brands: []uuid.UUID{brand}},
		{name: "absent brand", brands: []uuid.UUID{uuid.Nil}},
	}
}

func assertSameCategoryPage(t *testing.T, tx pgx.Tx, q *db.Queries, p db.CategoryListingParams) []db.CategoryListingRow {
	t.Helper()
	rows, err := tx.Query(t.Context(), categoryBeforePagination, p.Locale, p.CategoryIds, p.BrandIds, p.FilterVariants, p.InStockOnly, p.MinPrice, p.MaxPrice, p.Sort, p.PageOffset, p.PageSize)
	if err != nil {
		t.Fatal(err)
	}
	want, err := pgx.CollectRows(rows, pgx.RowToStructByPos[db.CategoryListingRow])
	if err != nil {
		t.Fatal(err)
	}
	got, err := q.CategoryListing(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("category identities/order/presentation changed:\ngot %+v\nwant %+v", got, want)
	}
	var wantCount int64
	if err := tx.QueryRow(t.Context(), categoryCountBeforePagination, p.CategoryIds, p.BrandIds, p.FilterVariants, p.InStockOnly, p.MinPrice, p.MaxPrice).Scan(&wantCount); err != nil {
		t.Fatal(err)
	}
	gotCount, err := q.CategoryListingCount(t.Context(), db.CategoryListingCountParams{CategoryIds: p.CategoryIds, BrandIds: p.BrandIds, FilterVariants: p.FilterVariants, InStockOnly: p.InStockOnly, MinPrice: p.MinPrice, MaxPrice: p.MaxPrice})
	if err != nil || gotCount != wantCount {
		t.Fatalf("category count=%d error=%v, want %d", gotCount, err, wantCount)
	}
	return got
}

func assertCategoryLivePrices(t *testing.T, tx pgx.Tx, q *db.Queries, ids []uuid.UUID) {
	t.Helper()
	params := db.CategoryListingParams{Locale: "en", Sort: "price_asc", CategoryIds: ids, BrandIds: []uuid.UUID{}, FilterVariants: true, MinPrice: 100, MaxPrice: 175, PageSize: catalog.PageSize}
	before := assertSameCategoryPage(t, tx, q, params)
	if len(before) != 2 || before[0].Slug != "scale-00001" || before[0].MinPriceCents != 150 || before[1].Slug != "scale-00002" || before[1].MinPriceCents != 200 {
		t.Fatalf("buyable-first display price must be independent of matching filter variant: %+v", before)
	}
	params.InStockOnly = true
	stocked := assertSameCategoryPage(t, tx, q, params)
	if len(stocked) != 1 || stocked[0].Slug != "scale-00001" {
		t.Fatalf("one variant must satisfy stock and price together: %+v", stocked)
	}
	if _, err := tx.Exec(t.Context(), `
UPDATE product_variants SET price_cents=price_cents+111 WHERE sku='SC-00001-0';
SELECT record_inventory_movement(id,-stock_quantity,'adjustment','category-page-live-stock')
FROM product_variants WHERE sku='SC-00002-1';`); err != nil {
		t.Fatal(err)
	}
	params.InStockOnly = false
	params.FilterVariants = false
	after := assertSameCategoryPage(t, tx, q, params)
	if len(after) < 2 || after[0].Slug != "scale-00002" || after[0].InStock || after[0].MinPriceCents != 100 || after[1].Slug != "scale-00001" || after[1].MinPriceCents != 261 {
		t.Fatalf("page must reflect live price/stock and reorder: %+v", after)
	}
}
