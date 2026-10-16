//go:build integration

// Package productstest builds the catalogue desk for integration suites, its
// own and those of the desks whose tests need a product or the desk's routes.
// It carries the integration tag, so no production build sees it.
package productstest

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/media"
)

// Draft creates a draft product through the catalogue desk, with a brand and
// a category read from pool, and returns its slug.
func Draft(t *testing.T, ctx context.Context, pool *pgxpool.Pool, s *products.Store) string {
	t.Helper()

	var brandID, catID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM brands LIMIT 1`).Scan(&brandID); err != nil {
		t.Fatalf("read a brand: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM categories LIMIT 1`).Scan(&catID); err != nil {
		t.Fatalf("read a category: %v", err)
	}
	slug := "spec-" + uuid.New().String()[:8]
	form := &products.Form{
		Slug: slug, Name: "規格表測試 " + slug, Summary: "測試用",
		Description: "測試用商品", BrandID: brandID, CategoryID: catID,
	}
	created, errs, err := s.Create(ctx, form)
	if err != nil || len(errs) > 0 {
		t.Fatalf("Create: %v %v", err, errs)
	}
	return created
}

// PostVariantForm posts form to h's add-variant handler for the product slug.
func PostVariantForm(
	t *testing.T, h *products.Handler, ctx context.Context, slug string, form url.Values,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/products/"+slug+"/variants", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	res := httptest.NewRecorder()
	h.AddVariant(res, req)
	return res
}

// NewHandler serves s with its uploads stored over p, the pool s was built over.
func NewHandler(p *pgxpool.Pool, s *products.Store) *products.Handler {
	log := slog.New(slog.DiscardHandler)
	return products.NewHandler(s, media.NewHandler(media.NewStore(p), log), log)
}
