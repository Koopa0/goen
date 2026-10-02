//go:build integration

package catalog_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/ui/pages"
)

// skuFixture is one product the SKU search may or may not return.
type skuFixture struct {
	name, brand, summary, spec string
	skus                       []string // active variants
	retiredSKUs                []string
}

func TestSearchSKUsRankAndPaginateWithoutDuplicateProducts(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(ctx)) })
	token := "SKU" + strings.ToUpper(uuid.NewString()[:8])
	other := func() string { return "OTHER-" + strings.ToUpper(uuid.NewString()) }
	generic := "Receipt lookup fixture"

	// Oldest first: publication order is the reverse of the ranking that is
	// locked, so only the ranking arms can put a product where it belongs.
	partialOnly := catalog.PageSize + 1 - 6
	fixtures := make([]skuFixture, 0, catalog.PageSize+1)
	fixtures = append(fixtures,
		skuFixture{name: generic, skus: []string{token}},              // exact SKU
		skuFixture{name: token, skus: []string{other()}},              // exact name
		skuFixture{name: token + " edition", skus: []string{other()}}, // partial name
	)
	for i := range partialOnly {
		fixtures = append(fixtures, skuFixture{name: generic, skus: []string{fmt.Sprintf("%s-%02d", token, i)}}) // partial SKU only
	}
	fixtures = append(fixtures,
		skuFixture{name: generic, brand: token, skus: []string{other()}},   // brand only, newer than every partial SKU
		skuFixture{name: generic, summary: token, skus: []string{other()}}, // summary only
		skuFixture{name: generic, spec: token, skus: []string{other()}},    // spec only
	)
	// Matches by SKU text, but only on retired variants: never a result.
	retired := skuFixture{name: generic, skus: []string{"CURRENT-" + strings.ToUpper(uuid.NewString())}, retiredSKUs: []string{token + "-RETIRED", token + "-RETIRED-2"}}

	insert := func(f skuFixture, i int, status string) string { return insertSKUFixture(ctx, t, tx, f, i, status) }
	slugs := make([]string, len(fixtures))
	for i, f := range fixtures {
		slugs[i] = insert(f, i, "active")
	}
	retiredSlug := insert(retired, len(fixtures), "active")

	privateSlug := "sku-private-" + uuid.NewString()
	var privateID uuid.UUID
	if fixtureErr := tx.QueryRow(ctx, `INSERT INTO products (brand_id, category_id, slug, name, status)
 SELECT brand_id, category_id, $1, 'Private receipt fixture', 'draft' FROM products LIMIT 1 RETURNING id`, privateSlug).Scan(&privateID); fixtureErr != nil {
		t.Fatal(fixtureErr)
	}
	if _, fixtureErr := tx.Exec(ctx, `INSERT INTO product_variants (product_id, sku, price_cents) VALUES ($1, $2, 10000)`, privateID, token+"-PRIVATE"); fixtureErr != nil {
		t.Fatal(fixtureErr)
	}

	// Exact SKU, exact name, partial name, partial SKU (newest first), then the
	// brand, summary and spec matches, which are newer than every partial SKU.
	want := []string{slugs[0], slugs[1], slugs[2]}
	for i := 3 + partialOnly - 1; i >= 3; i-- {
		want = append(want, slugs[i])
	}
	brandSlug, summarySlug, specSlug := slugs[len(slugs)-3], slugs[len(slugs)-2], slugs[len(slugs)-1]
	want = append(want, brandSlug, summarySlug, specSlug)

	store := catalog.NewStore(tx)
	first, err := store.Search(ctx, catalog.SearchPattern(token), catalog.SortRelevance, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Search(ctx, catalog.SearchPattern(token), catalog.SortRelevance, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range [][]pages.ProductTile{first.Products, second.Products} {
		for _, product := range page {
			switch product.Slug {
			case privateSlug:
				t.Fatal("SKU search exposed a non-public product")
			case retiredSlug:
				t.Fatal("SKU search matched a product through retired variants only")
			}
		}
	}
	if first.Total != int64(len(slugs)) || len(first.Products) != catalog.PageSize || len(second.Products) != 1 {
		t.Fatalf("SKU pagination total=%d first=%d second=%d", first.Total, len(first.Products), len(second.Products))
	}
	got := append(skuResultSlugs(first.Products), skuResultSlugs(second.Products)...)
	for i, slug := range want {
		if got[i] != slug {
			t.Errorf("rank %d = %s, want %s (0 exact SKU, 1 exact name, 2 partial name, then partial SKUs, brand, summary, spec)", i, got[i], slug)
		}
	}
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/search?q="+strings.ToLower(token), http.NoBody)
	response := httptest.NewRecorder()
	catalog.NewHandler(store, slog.New(slog.DiscardHandler)).Search(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "/p/"+slugs[0]) {
		t.Fatalf("receipt SKU HTTP lookup status=%d omits exact product", response.Code)
	}
	seen := map[string]bool{}
	for _, slug := range got {
		if seen[slug] {
			t.Errorf("product %s is duplicated across SKU results", slug)
		}
		seen[slug] = true
	}
}

func insertSKUFixture(ctx context.Context, t *testing.T, tx pgx.Tx, f skuFixture, i int, status string) string {
	t.Helper()
	slug := "sku-search-" + uuid.NewString()
	var brandID, id uuid.UUID
	if f.brand != "" {
		if fixtureErr := tx.QueryRow(ctx, `INSERT INTO brands (slug, name) VALUES ($1, $2) RETURNING id`, "brand-"+uuid.NewString(), f.brand).Scan(&brandID); fixtureErr != nil {
			t.Fatal(fixtureErr)
		}
	}
	if fixtureErr := tx.QueryRow(ctx, `INSERT INTO products (brand_id, category_id, slug, name, summary, status, published_at)
   SELECT COALESCE($5, brand_id), category_id, $1, $2, NULLIF($3, ''), 'draft', now() + ($4 * interval '1 second') FROM products LIMIT 1 RETURNING id`, slug, f.name, f.summary, i, uuid.NullUUID{UUID: brandID, Valid: f.brand != ""}).Scan(&id); fixtureErr != nil {
		t.Fatal(fixtureErr)
	}
	for pos, sku := range append(append([]string{}, f.skus...), f.retiredSKUs...) {
		if _, fixtureErr := tx.Exec(ctx, `INSERT INTO product_variants (product_id, sku, price_cents, position, is_active) VALUES ($1, $2, 10000, $3, $4)`, id, sku, pos, pos < len(f.skus)); fixtureErr != nil {
			t.Fatal(fixtureErr)
		}
	}
	if f.spec != "" {
		if _, fixtureErr := tx.Exec(ctx, `INSERT INTO product_specs (product_id, label, value, position) VALUES ($1, 'Lookup', $2, 0)`, id, f.spec); fixtureErr != nil {
			t.Fatal(fixtureErr)
		}
	}
	if status != "draft" {
		if _, fixtureErr := tx.Exec(ctx, `UPDATE products SET status = $2 WHERE id = $1`, id, status); fixtureErr != nil {
			t.Fatal(fixtureErr)
		}
	}
	return slug
}

// A retired variant carries no ranking either: these products match only
// through their summary, so their retired SKUs must not lift them above the
// exact-name and brand matches.
func TestRetiredVariantsNeverLiftARankedSKUMatch(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(ctx)) })
	token := "SKU" + strings.ToUpper(uuid.NewString()[:8])
	other := "OTHER-" + strings.ToUpper(uuid.NewString())
	generic := "Receipt lookup fixture"
	exactName := insertSKUFixture(ctx, t, tx, skuFixture{name: token, skus: []string{other}}, 0, "active")
	retiredExact := insertSKUFixture(ctx, t, tx, skuFixture{name: generic, summary: token, skus: []string{"CURRENT-" + strings.ToUpper(uuid.NewString())}, retiredSKUs: []string{token}}, 1, "active")
	retiredPartial := insertSKUFixture(ctx, t, tx, skuFixture{name: generic, summary: token, skus: []string{"CURRENT-" + strings.ToUpper(uuid.NewString())}, retiredSKUs: []string{token + "-OLD"}}, 2, "active")
	brand := insertSKUFixture(ctx, t, tx, skuFixture{name: generic, brand: token, skus: []string{"CURRENT-" + strings.ToUpper(uuid.NewString())}}, 3, "active")
	view, err := catalog.NewStore(tx).Search(ctx, catalog.SearchPattern(token), catalog.SortRelevance, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{exactName, brand, retiredPartial, retiredExact}
	got := skuResultSlugs(view.Products)
	if len(got) != len(want) {
		t.Fatalf("results = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("rank %d = %s, want %s (exact name, brand, then the summary matches newest first)", i, got[i], want[i])
		}
	}
}

func skuResultSlugs(products []pages.ProductTile) []string {
	out := make([]string, 0, len(products))
	for i := range products {
		out = append(out, products[i].Slug)
	}
	return out
}
