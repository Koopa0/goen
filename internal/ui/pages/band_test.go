package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestBandPhotoFallsBackToTheFirstProductThenToNothing(t *testing.T) {
	t.Parallel()

	own := Photo{URL: "/dept.webp"}
	first := ProductTile{ImageURL: "/first.webp", ImageSrcset: "/first-400.webp 400w", ImageAlt: "first"}
	second := ProductTile{ImageURL: "/second.webp"}

	for name, tt := range map[string]struct {
		department Photo
		products   []ProductTile
		wantURL    string
		wantWell   bool
	}{
		"department photograph":   {own, []ProductTile{first}, "/dept.webp", false},
		"first product on a well": {Photo{}, []ProductTile{first, second}, "/first.webp", true},
		"first product has none":  {Photo{}, []ProductTile{{}, second}, "", false},
		"no products":             {Photo{}, nil, "", false},
	} {
		got, onWell := bandPhoto(tt.department, tt.products)
		if got.URL != tt.wantURL || onWell != tt.wantWell {
			t.Errorf("%s: bandPhoto = (%q, %v), want (%q, %v)", name, got.URL, onWell, tt.wantURL, tt.wantWell)
		}
	}
}

func TestBandNameStepsDownPastItsLimit(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]bool{
		"3C 數位":                 false,
		"居家生活與廚房":               true,
		"六個字的名字":                false,
		"Tech":                  false,
		"Home and living goods": true,
		"Eighteen letters!!":    false,
	} {
		if got := bandNameLong(name); got != want {
			t.Errorf("bandNameLong(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestTheHomeBandTakesTheWellAndTheStepDown(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	tiles := []ProductTile{{Slug: "a", Name: "A", ImageURL: "/a.webp"}}
	page := renderComponent(t, ctx, Home(layouts.Page{}, HomeView{Band: &DepartmentBand{
		Name: "居家生活與廚房", Href: "/c/home", Tone: ToneInk, Tiles: tiles,
	}}))
	for _, want := range []string{
		`class="goen-band" data-tone="ink"`,
		`class="goen-band__media goen-band__media--well"`,
		`goen-home__heading goen-band__name--long`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("home band lacks %q", want)
		}
	}

	plain := renderComponent(t, ctx, Home(layouts.Page{}, HomeView{Band: &DepartmentBand{
		Name: "3C 數位", Href: "/c/tech", Tone: ToneMist, Photo: Photo{URL: "/tech.webp"}, Tiles: tiles,
	}}))
	if strings.Contains(plain, "goen-band__media--well") || strings.Contains(plain, "name--long") {
		t.Error("a department photograph on a short name took the well or the step-down")
	}
}

func TestADepartmentHeadWithNoPhotographAndNoProductDrawsNone(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)

	page := renderComponent(t, ctx, Listing(layouts.Page{}, ListingView{Slug: "c", Name: "Books", Theme: &Theme{Tone: ToneBlush}}, nil, nil))
	if strings.Contains(page, "goen-pagehead__photo") {
		t.Error("a head with neither a department nor a product photograph drew an image")
	}
	withProduct := renderComponent(t, ctx, Listing(layouts.Page{}, ListingView{Slug: "c", Name: "Books", Products: []ProductTile{{Slug: "p", Name: "P", ImageURL: "/p.webp"}}}, nil, nil))
	if !strings.Contains(withProduct, `class="goen-band__media goen-band__media--well"`) {
		t.Error("a head with no department photograph did not take its first product's, on the well")
	}
}

func TestTheDepartmentHeadIsTheBand(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)

	page := renderComponent(t, ctx, Listing(layouts.Page{}, ListingView{Slug: "c", Name: "Books", Theme: &Theme{Tone: ToneSage, Photo: Photo{URL: "/d.webp"}}}, nil, nil))
	band := strings.Index(page, `class="goen-band"`)
	grid := strings.Index(page, `class="goen-band__grid"`)
	media := strings.Index(page, `class="goen-band__media"`)
	body := strings.Index(page, `class="goen-band__body goen-pagehead__text"`)
	if band < 0 || !(band < grid && grid < media && media < body) {
		t.Errorf("the head is not band, grid, media (the photograph first), body in that order:\n%s", page)
	}
}
