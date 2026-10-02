//go:build integration

package campaigns_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/campaigns"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/i18n"
)

func TestCampaignEnglishTitleReachesStorefront(t *testing.T) {
	for _, titleEn := range []string{"Summer selection", ""} {
		ctx, _ := admintest.StaffContext(t, pool)
		store := campaigns.NewStore(pool)
		handler := handlerOver(store)
		slug := admintest.CampaignSlug(t)
		body := url.Values{"slug": {slug}, "title": {"Original campaign"}, "title_en": {titleEn}, "days": {"7"}}
		request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/campaigns", strings.NewReader(body.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		handler.Create(response, request)
		if response.Code != http.StatusSeeOther {
			t.Fatalf("create status = %d: %s", response.Code, response.Body.String())
		}
		if err := store.FeatureProduct(ctx, slug, admintest.DiscountedProductSlug(t, pool)); err != nil {
			t.Fatal(err)
		}
		if err := store.SetActive(ctx, slug, true); err != nil {
			t.Fatal(err)
		}
		view, err := catalog.NewStore(pool).Campaign(i18n.WithLocale(ctx, i18n.En), slug)
		if err != nil {
			t.Fatal(err)
		}
		want := titleEn
		if want == "" {
			want = "Original campaign"
		}
		if view.Title != want {
			t.Errorf("English storefront title = %q, want %q", view.Title, want)
		}
	}
}

func TestCampaignEnglishTitleRefusalPreservesDraft(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	handler := handlerOver(campaigns.NewStore(pool))
	title := strings.Repeat("e", 61)
	body := url.Values{"slug": {admintest.CampaignSlug(t)}, "title": {"Original"}, "title_en": {title}, "days": {"7"}}
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/campaigns", strings.NewReader(body.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.Create(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", response.Code)
	}
	for _, want := range []string{`name="title_en"`, `value="` + title + `"`, `id="k-title-en-error"`, `aria-describedby="k-title-en-hint k-title-en-error"`, `aria-invalid="true"`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("refused form omits %s", want)
		}
	}
}
