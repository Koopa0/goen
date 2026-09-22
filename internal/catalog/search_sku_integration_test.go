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

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestSearchSKUsRankAndPaginateWithoutDuplicateProducts(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(ctx)) })
	token := "SKU" + strings.ToUpper(uuid.NewString()[:8])
	slugs := make([]string, catalog.PageSize+1)
	for i := range slugs {
		slugs[i] = "sku-search-" + uuid.NewString()
		name := "Receipt lookup fixture"
		sku := fmt.Sprintf("%s-%02d", token, i)
		if i == 0 {
			sku = token
		}
		if i == 1 {
			name = token + " edition"
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
   SELECT brand_id, category_id, $1, $2, 'draft', now() + ($3 * interval '1 second') FROM products LIMIT 1 RETURNING id`, slugs[i], name, i).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO product_variants (product_id, sku, price_cents, position) VALUES ($1, $2, 10000, 0), ($1, $3, 20000, 1)`, id, sku, sku+"-OTHER"); err != nil {
			t.Fatal(err)
		}
		switch i {
		case 0:
			if _, err := tx.Exec(ctx, `UPDATE product_variants SET is_active = false WHERE product_id = $1 AND sku = $2`, id, sku); err != nil {
				t.Fatal(err)
			}
		case len(slugs) - 1:
			if _, err := tx.Exec(ctx, `INSERT INTO product_variants (product_id, sku, price_cents, position) VALUES ($1, $2, 10000, 2)`, id, "CURRENT-"+strings.ToUpper(uuid.NewString())); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, `UPDATE product_variants SET is_active = false WHERE product_id = $1 AND position < 2`, id); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE products SET status = 'active' WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	privateSlug := "sku-private-" + uuid.NewString()
	var privateID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO products (brand_id, category_id, slug, name, status)
 SELECT brand_id, category_id, $1, 'Private receipt fixture', 'draft' FROM products LIMIT 1 RETURNING id`, privateSlug).Scan(&privateID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO product_variants (product_id, sku, price_cents) VALUES ($1, $2, 10000)`, privateID, token+"-PRIVATE"); err != nil {
		t.Fatal(err)
	}
	store := catalog.NewStore(tx)
	first, err := store.Search(ctx, catalog.SearchPattern(token), 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Search(ctx, catalog.SearchPattern(token), 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range [][]pages.ProductTile{first.Products, second.Products} {
		for _, product := range page {
			if product.Slug == privateSlug {
				t.Fatal("SKU search exposed a non-public product")
			}
		}
	}
	if first.Total != int64(len(slugs)) || len(first.Products) != catalog.PageSize || len(second.Products) != 1 {
		t.Fatalf("SKU pagination total=%d first=%d second=%d", first.Total, len(first.Products), len(second.Products))
	}
	if first.Products[0].Slug != slugs[0] || first.Products[1].Slug != slugs[1] {
		t.Fatalf("SKU/name ranking starts %s, %s", first.Products[0].Slug, first.Products[1].Slug)
	}
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/search?q="+strings.ToLower(token), http.NoBody)
	response := httptest.NewRecorder()
	catalog.NewHandler(store, slog.New(slog.DiscardHandler)).Search(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "/p/"+slugs[0]) {
		t.Fatalf("receipt SKU HTTP lookup status=%d omits exact product", response.Code)
	}
	seen := map[string]bool{}
	for _, page := range [][]string{skuResultSlugs(first.Products), skuResultSlugs(second.Products)} {
		for _, slug := range page {
			if seen[slug] {
				t.Errorf("product %s is duplicated across SKU results", slug)
			}
			seen[slug] = true
		}
	}
}

func skuResultSlugs(products []pages.ProductTile) []string {
	out := make([]string, 0, len(products))
	for _, product := range products {
		out = append(out, product.Slug)
	}
	return out
}
