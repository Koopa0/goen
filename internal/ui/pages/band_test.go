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

func TestTheHomeBandIsAShelfWithNoPhotograph(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	page := renderComponent(t, ctx, Home(layouts.Page{}, HomeView{Band: &DepartmentBand{
		Name: "居家生活與廚房", Fact: "廚房 · 寢具", Items: 12, Href: "/c/home", Tone: ToneInk, Tiles: tiledShelf(4),
	}}))
	for _, want := range []string{
		`class="goen-band goen-band--shelf" data-tone="ink"`,
		`<h2 id="band-heading" class="goen-band__name">居家生活與廚房</h2>`,
		`<p class="goen-band__fact">廚房 · 寢具</p>`,
		`href="/c/home"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("home band lacks %q", want)
		}
	}
	start := strings.Index(page, `class="goen-band goen-band--shelf"`)
	if start < 0 {
		t.Fatal("home band is not drawn as a shelf")
	}
	band := page[start:]
	for _, not := range []string{"goen-band__media", "goen-band__photo", "<img class=\"goen-band"} {
		if strings.Contains(band, not) {
			t.Errorf("home band draws %s, which belongs to the department page's head", not)
		}
	}
	if got := strings.Count(band, `class="goen-tile`); got < 4 {
		t.Errorf("home band draws %d tile classes, want its four products", got)
	}
}

func TestTheHomeBandWithNoSubCategoriesDrawsNoFactLine(t *testing.T) {
	t.Parallel()
	page := renderComponent(t, i18n.WithLocale(t.Context(), i18n.En), Home(layouts.Page{}, HomeView{Band: &DepartmentBand{
		Name: "Tech", Href: "/c/tech", Tiles: tiledShelf(4),
	}}))
	if strings.Contains(page, "goen-band__fact") {
		t.Error("a band with no sub-categories drew an empty fact line")
	}
}

func TestTheDepartmentBandFollowsTheDirectory(t *testing.T) {
	t.Parallel()
	page := renderIn(t, i18n.ZhHant, Home(layouts.Page{}, HomeView{
		Categories: departmentsOf(2),
		Row:        ProductRow{Title: "新到貨", Href: "/search", Tiles: tiledShelf(4)},
		Band:       &DepartmentBand{Name: "館0", Href: "/c/d0", Tiles: tiledShelf(4)},
	}))
	row := strings.Index(page, `id="row-heading"`)
	cats := strings.Index(page, `id="cats-heading"`)
	band := strings.Index(page, `id="band-heading"`)
	if row < 0 || cats < row || band < cats {
		t.Errorf("section order is row %d, departments %d, band %d; want the row, then the departments, then the band", row, cats, band)
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
	if band < 0 || band >= grid || grid >= media || media >= body {
		t.Errorf("the head is not band, grid, media (the photograph first), body in that order:\n%s", page)
	}
	if want := `sizes="(min-width: 48rem) 57vw, 100vw"`; !strings.Contains(page, want) {
		t.Errorf("the head photograph does not carry %s:\n%s", want, page)
	}
	if strings.Contains(page, `fetchpriority="low"`) {
		t.Errorf("the head photograph is the first screen's largest image and must not be fetched at low priority:\n%s", page)
	}
}
