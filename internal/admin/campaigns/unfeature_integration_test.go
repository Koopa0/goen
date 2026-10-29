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

// TestStore_UnfeatureProductMiss: a removal that removes nothing is refused and
// writes no campaign.unfeature row, because the trail lists only changes that
// happened.
func TestStore_UnfeatureProductMiss(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := campaigns.NewStore(pool)
	slug := admintest.CampaignSlug(t)
	if _, err := s.Create(ctx, &campaigns.Form{
		Slug: slug, Title: "測試活動", Days: 7,
	}); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	productSlug := admintest.DiscountedProductSlug(t, pool)

	for _, tt := range []struct {
		name, campaign, product string
	}{
		{name: "NotFeatured", campaign: slug, product: productSlug},
		{name: "UnknownProduct", campaign: slug, product: "no-such-product-" + uuid.NewString()},
		{name: "UnknownCampaign", campaign: "no-such-campaign-" + uuid.NewString(), product: productSlug},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := admintest.AuditRows(t, pool, audit.ActionUnfeatureProduct)
			if err := s.UnfeatureProduct(ctx, tt.campaign, tt.product); !errors.Is(err, campaigns.ErrNotFound) {
				t.Errorf("UnfeatureProduct(%q, %q) = %v, want ErrNotFound", tt.campaign, tt.product, err)
			}
			if after := admintest.AuditRows(t, pool, audit.ActionUnfeatureProduct); after != before {
				t.Errorf("%d campaign.unfeature rows after a removal that removed nothing, want %d", after, before)
			}
		})
	}
}

// TestStore_UnfeatureProduct: a removal that took writes one row, and the same
// removal sent again, as a resubmitted form does, writes none.
func TestStore_UnfeatureProduct(t *testing.T) {
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
		t.Fatalf("feature: %v", err)
	}

	before := admintest.AuditRows(t, pool, audit.ActionUnfeatureProduct)
	if err := s.UnfeatureProduct(ctx, slug, productSlug); err != nil {
		t.Fatalf("UnfeatureProduct(%q, %q) = %v, want nil", slug, productSlug, err)
	}
	var left int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM sale_campaign_products cp
		JOIN sale_campaigns c ON c.id = cp.campaign_id
		WHERE c.slug = $1`, slug).Scan(&left); err != nil {
		t.Fatalf("count campaign products: %v", err)
	}
	if left != 0 {
		t.Errorf("%d products left on the campaign after a removal, want 0", left)
	}
	if after := admintest.AuditRows(t, pool, audit.ActionUnfeatureProduct); after != before+1 {
		t.Errorf("%d campaign.unfeature rows after one removal, want %d", after, before+1)
	}

	if err := s.UnfeatureProduct(ctx, slug, productSlug); !errors.Is(err, campaigns.ErrNotFound) {
		t.Errorf("second UnfeatureProduct = %v, want ErrNotFound", err)
	}
	if again := admintest.AuditRows(t, pool, audit.ActionUnfeatureProduct); again != before+1 {
		t.Errorf("%d campaign.unfeature rows after a repeated removal, want %d", again, before+1)
	}
}

// TestHandler_UnfeatureProductMiss: staff who remove what is not there are told
// it was refused and never that it was removed.
func TestHandler_UnfeatureProductMiss(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := campaigns.NewStore(pool)
	h := handlerOver(s)
	slug := admintest.CampaignSlug(t)
	productSlug := admintest.DiscountedProductSlug(t, pool)
	if _, err := s.Create(ctx, &campaigns.Form{
		Slug: slug, Title: "測試活動", Days: 7,
	}); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	if err := s.FeatureProduct(ctx, slug, productSlug); err != nil {
		t.Fatalf("feature: %v", err)
	}

	remove := func(campaign, product string) *httptest.ResponseRecorder {
		body := url.Values{"product": {product}, "action": {"remove"}}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/admin/campaigns/"+campaign+"/products", strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("slug", campaign)
		res := httptest.NewRecorder()
		h.FeatureProduct(res, req)
		return res
	}

	if res := remove(slug, productSlug); res.Code != http.StatusSeeOther ||
		!strings.Contains(res.Header().Get("Location"), "?unfeatured=1") {
		t.Fatalf("first removal redirect = %d %q, want unfeatured=1", res.Code, res.Header().Get("Location"))
	}
	for name, res := range map[string]*httptest.ResponseRecorder{
		"repeated":         remove(slug, productSlug),
		"unknown campaign": remove("no-such-campaign-"+uuid.NewString(), productSlug),
	} {
		if res.Code != http.StatusSeeOther ||
			!strings.Contains(res.Header().Get("Location"), "?refused=1") ||
			strings.Contains(res.Header().Get("Location"), "?unfeatured=1") {
			t.Errorf("%s removal redirect = %d %q, want refused without unfeatured",
				name, res.Code, res.Header().Get("Location"))
		}
	}
}
