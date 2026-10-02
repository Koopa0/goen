//go:build integration

package campaigns_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/campaigns"
)

func TestFeatureProductUnknownProductReturnsNotFound(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := campaigns.NewStore(pool)
	slug := admintest.CampaignSlug(t)
	if _, err := s.Create(ctx, &campaigns.Form{
		Slug: slug, Title: "測試活動", Days: 7,
	}); err != nil {
		t.Fatalf("create campaign: %v", err)
	}

	missing := "no-such-product-" + uuid.NewString()
	before := admintest.AuditRows(t, pool, audit.ActionFeatureProduct)
	if err := s.FeatureProduct(ctx, slug, missing); !errors.Is(err, campaigns.ErrNotFound) {
		t.Fatalf("unknown product = %v, want ErrNotFound", err)
	}
	if after := admintest.AuditRows(t, pool, audit.ActionFeatureProduct); after != before {
		t.Errorf("%d audit rows after a zero-row feature, want %d", after, before)
	}
}

func TestFeatureProductAlreadyFeaturedReturnsNotFound(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := campaigns.NewStore(pool)
	slug := admintest.CampaignSlug(t)
	productSlug := admintest.DiscountedProductSlug(t, pool)
	if _, err := s.Create(ctx, &campaigns.Form{
		Slug: slug, Title: "測試活動", Days: 7,
	}); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	if err := s.FeatureProduct(ctx, slug, productSlug); err != nil {
		t.Fatalf("first feature: %v", err)
	}
	if err := s.FeatureProduct(ctx, slug, productSlug); !errors.Is(err, campaigns.ErrNotFound) {
		t.Fatalf("already featured = %v, want ErrNotFound", err)
	}
}

func TestFeatureProductHandlerNeverOKOnMiss(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := campaigns.NewStore(pool)
	h := handlerOver(s)
	slug := admintest.CampaignSlug(t)
	if _, err := s.Create(ctx, &campaigns.Form{
		Slug: slug, Title: "測試活動", Days: 7,
	}); err != nil {
		t.Fatalf("create campaign: %v", err)
	}

	post := func(product string) *httptest.ResponseRecorder {
		body := url.Values{"product": {product}}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/admin/campaigns/"+slug+"/products", strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("slug", slug)
		res := httptest.NewRecorder()
		h.FeatureProduct(res, req)
		return res
	}

	missing := "no-such-product-" + uuid.NewString()
	if res := post(missing); res.Code != http.StatusSeeOther ||
		!strings.Contains(res.Header().Get("Location"), "?refused=1") ||
		strings.Contains(res.Header().Get("Location"), "?ok=1") {
		t.Fatalf("unknown product redirect = %d %q, want refused without ok",
			res.Code, res.Header().Get("Location"))
	}

	productSlug := admintest.DiscountedProductSlug(t, pool)
	if ok := post(productSlug); ok.Code != http.StatusSeeOther ||
		!strings.Contains(ok.Header().Get("Location"), "?ok=1") {
		t.Fatalf("first feature redirect = %d %q, want ok=1", ok.Code, ok.Header().Get("Location"))
	}
	if again := post(productSlug); again.Code != http.StatusSeeOther ||
		!strings.Contains(again.Header().Get("Location"), "?refused=1") ||
		strings.Contains(again.Header().Get("Location"), "?ok=1") {
		t.Fatalf("already featured redirect = %d %q, want refused without ok",
			again.Code, again.Header().Get("Location"))
	}
}
