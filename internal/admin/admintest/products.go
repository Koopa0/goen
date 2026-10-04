//go:build integration

package admintest

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/media"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/products"
)

func AnyProductSlug(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var slug string
	if err := pool.QueryRow(t.Context(),
		`SELECT slug FROM products LIMIT 1`).Scan(&slug); err != nil {
		t.Fatalf("find product: %v", err)
	}
	return slug
}

// DraftProduct creates a draft product through the catalogue desk, with a brand
// and a category read from pool.
func DraftProduct(t *testing.T, ctx context.Context, pool *pgxpool.Pool, s *products.Store) string {
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

// ProductDesk serves s with its uploads stored over p, the pool s was built over.
func ProductDesk(p *pgxpool.Pool, s *products.Store) *products.Handler {
	log := slog.New(slog.DiscardHandler)
	return products.NewHandler(s, media.NewHandler(media.NewStore(p), log), log)
}
