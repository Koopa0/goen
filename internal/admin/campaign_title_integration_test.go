//go:build integration

package admin_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/i18n"
)

func TestCampaignEnglishTitleReachesStorefront(t *testing.T) {
	for _, titleEn := range []string{"Summer selection", ""} {
		ctx, _ := staffContext(t)
		store := admin.NewStore(pool, fakeRefunder{}, nil, nil)
		handler := adminHandlerOver(pool, store)
		slug := testCampaignSlug(t)
		body := url.Values{"slug": {slug}, "title": {"Original campaign"}, "title_en": {titleEn}, "days": {"7"}}
		request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/campaigns", strings.NewReader(body.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		handler.CreateCampaign(response, request)
		if response.Code != http.StatusSeeOther {
			t.Fatalf("create status = %d: %s", response.Code, response.Body.String())
		}
		if err := store.FeatureProduct(ctx, slug, discountedProductSlug(t)); err != nil {
			t.Fatal(err)
		}
		if err := store.SetCampaignActive(ctx, slug, true); err != nil {
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
	ctx, _ := staffContext(t)
	handler := adminHandlerOver(pool, admin.NewStore(pool, fakeRefunder{}, nil, nil))
	title := strings.Repeat("e", 61)
	body := url.Values{"slug": {testCampaignSlug(t)}, "title": {"Original"}, "title_en": {title}, "days": {"7"}}
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/campaigns", strings.NewReader(body.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.CreateCampaign(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", response.Code)
	}
	for _, want := range []string{`name="title_en"`, `value="` + title + `"`, `id="k-title-en-error"`, `aria-describedby="k-title-en-hint k-title-en-error"`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("refused form omits %s", want)
		}
	}
}
