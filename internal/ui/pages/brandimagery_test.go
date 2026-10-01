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
	if !strings.Contains(page, `alt="" width="800" height="800"`) {
		t.Error("the phones tile photograph must carry an empty alt: the name beside it is its label")
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

func renderHead(t *testing.T, p layouts.Page) string {
	t.Helper()
	ctx := layouts.WithSiteOrigin(i18n.WithLocale(t.Context(), i18n.ZhHant), "https://goen.test")
	var b strings.Builder
	if err := layouts.Base(p).Render(ctx, &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

func TestProductSharesItsOwnPhotographAndOtherPagesShareTheDefault(t *testing.T) {
	t.Parallel()
	with := renderHead(t, ProductMeta(&ProductView{Name: "Phone", Brand: "goen", Images: []ProductImage{
		{URL: "/static/media/products/p-1600.webp?v=abc", Alt: "front", Width: 1600, Height: 1200},
		{URL: "/static/media/products/q.webp?v=abc"},
	}}))
	if got := strings.Count(with, `property="og:image"`); got != 1 {
		t.Fatalf("a product page carries %d og:image tags, want 1", got)
	}
	for _, want := range []string{
		`<meta property="og:image" content="https://goen.test/static/media/products/p-1600.webp?v=abc"`,
		`og:image:width" content="1600"`, `og:image:height" content="1200"`, `og:image:alt" content="front"`,
	} {
		if !strings.Contains(with, want) {
			t.Errorf("product head omits %s", want)
		}
	}

	bare := renderHead(t, ProductMeta(&ProductView{Name: "Phone", Brand: "goen", Images: []ProductImage{{URL: "/x.webp"}}}))
	if strings.Contains(bare, "og:image:width") || strings.Contains(bare, "og:image:height") {
		t.Error("unknown dimensions must be left out, not stated as 0")
	}
	if !strings.Contains(bare, `og:image:alt" content="Phone"`) {
		t.Error("an image with no alt falls back to the product name")
	}

	none := renderHead(t, ProductMeta(&ProductView{Name: "Phone", Brand: "goen"}))
	if !strings.Contains(none, `content="https://goen.test`+assets.URL(assets.OGDefaultImage)+`"`) ||
		strings.Count(none, `property="og:image"`) != 1 {
		t.Error("a product with no photograph shares the default picture")
	}
}
