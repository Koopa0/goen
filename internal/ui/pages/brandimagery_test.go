package pages

import (
	"html"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func renderIn(t *testing.T, locale i18n.Locale, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(i18n.WithLocale(t.Context(), locale), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

func TestAboutShowsItsPhotographWithALocalizedAlt(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		page := renderIn(t, locale, About(AboutMeta(ctx)))
		for _, want := range []string{
			`src="` + assets.URL(assets.AboutImage) + `"`,
			`about-01-400.webp`,
			`alt="` + html.EscapeString(i18n.T(ctx, i18n.KeyAboutImageAlt)) + `"`,
		} {
			if !strings.Contains(page, want) {
				t.Errorf("%s about page omits %s", locale, want)
			}
		}
	}
}

func TestHomeCategoryTilesShowTheirPhotographAndKeepTheIconOtherwise(t *testing.T) {
	t.Parallel()
	page := renderIn(t, i18n.ZhHant, Home(layouts.Page{}, HomeView{Categories: []HomeCategory{
		{Slug: "phones", Name: "手機", IconKey: "phone"},
		{Slug: "unlisted", Name: "其他", IconKey: "plug"},
	}}))
	if !strings.Contains(page, `href="/c/phones"`) || !strings.Contains(page, "手機") {
		t.Fatal("the tile lost its link or its name")
	}
	if !strings.Contains(page, `src="`+assets.URL("media/categories/phones.webp")+`"`) ||
		!strings.Contains(page, "phones-400.webp") {
		t.Error("the phones tile does not show its photograph")
	}
	if strings.Count(page, `class="goen-cat__photo"`) != 1 || strings.Count(page, `class="goen-cat__icon"`) != 1 {
		t.Error("a category with no photograph must keep its icon, and only that one")
	}
}

func TestEveryCategoryPhotographIsEmbedded(t *testing.T) {
	t.Parallel()
	for slug := range map[string]bool{"phones": true, "laptops": true, "tablets": true, "audio": true, "wearables": true, "accessories": true} {
		if _, _, ok := assets.CategoryImage(slug); !ok {
			t.Errorf("no photograph for %s", slug)
		}
	}
	if _, _, ok := assets.CategoryImage("chargers"); ok {
		t.Error("a slug outside the closed set has a photograph")
	}
}

func TestEmptyStatesShowTheirIllustrationAsDecoration(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		page  string
		asset string
	}{
		"cart":            {renderIn(t, i18n.ZhHant, Cart(layouts.Page{}, CartView{})), assets.EmptyCartImage},
		"campaign":        {renderIn(t, i18n.ZhHant, Campaign(layouts.Page{}, CampaignView{Title: "x"})), assets.EmptyCampaignImage},
		"category":        {renderIn(t, i18n.ZhHant, Listing(layouts.Page{}, ListingView{Slug: "phones"})), assets.EmptySearchImage},
		"search prompt":   {renderIn(t, i18n.ZhHant, Search(layouts.Page{}, SearchView{})), assets.EmptySearchImage},
		"search no match": {renderIn(t, i18n.ZhHant, Search(layouts.Page{}, SearchView{Query: "zzz"})), assets.EmptySearchImage},
	} {
		want := `<img class="goen-empty__art" src="` + assets.URL(tt.asset) + `" alt="" width="128" height="96"`
		if !strings.Contains(tt.page, want) {
			t.Errorf("%s empty state omits %s", name, want)
		}
	}
}
