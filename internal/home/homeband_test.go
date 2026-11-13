package home

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/policy"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/hero"
	"github.com/koopa0/goen/internal/ui/tone"
)

// TestDepartmentBandShelf holds that the home band is drawn as a shelf with no
// photograph, its name a heading of its own.
func TestDepartmentBandShelf(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	page := renderComponent(t, ctx, Page(frame.Page{}, View{Band: &DepartmentBand{
		Name: "居家生活與廚房", Subs: []string{"廚房", "寢具"}, Items: 12, Href: "/c/home", Tone: tone.Ink, Tiles: tiledShelf(4),
	}}))
	for _, want := range []string{
		`class="goen-band" data-tone="ink"`,
		`<h2 id="band-heading" class="goen-band__name">居家生活與廚房</h2>`,
		`廚房&nbsp;<span class="goen-sep" aria-hidden="true">·</span> 寢具</p>`,
		`href="/c/home"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("home band lacks %q", want)
		}
	}
	start := strings.Index(page, `class="goen-band"`)
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

// TestDepartmentBandSubsNoBreak holds that the band's sub-categories are set
// apart as the design draws them: a space on each side of a dot that a screen
// reader skips, and a no-break space before the dot, so a line never starts with
// one.
func TestDepartmentBandSubsNoBreak(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		page := renderIn(t, locale, Page(frame.Page{}, View{Band: &DepartmentBand{
			Name: "Tech", Href: "/c/tech", Subs: []string{"Phones", "Laptops", "Accessories"}, Tiles: tiledShelf(4),
		}}))
		want := `Phones&nbsp;<span class="goen-sep" aria-hidden="true">·</span> Laptops&nbsp;<span class="goen-sep" aria-hidden="true">·</span> Accessories</p>`
		if !strings.Contains(page, want) {
			t.Errorf("%s: the band's sub-categories are not %s", locale, want)
		}
	}
}

// TestDepartmentBandNoFact holds that no fact line is drawn for a band whose
// department has no sub-categories.
func TestDepartmentBandNoFact(t *testing.T) {
	t.Parallel()
	page := renderComponent(t, i18n.WithLocale(t.Context(), i18n.En), Page(frame.Page{}, View{Band: &DepartmentBand{
		Name: "Tech", Href: "/c/tech", Tiles: tiledShelf(4),
	}}))
	if strings.Contains(page, "goen-band__fact") {
		t.Error("a band with no sub-categories drew an empty fact line")
	}
}

// TestPageSectionOrder holds the order of the home page's sections: the hero
// with its campaign's products, the product row when one is drawn, then the
// departments, the band and the shop rules last.
func TestPageSectionOrder(t *testing.T) {
	t.Parallel()
	page := renderWithNav(t, departmentsOf(2), Page(frame.Page{}, View{
		Slides: []hero.Slide{{Source: hero.SourceCampaign, Title: "茶與咖啡週", CTA: hero.CTA{Label: "看全部商品", Href: "/s/tea"}, Tiles: tiledShelf(4)}},
		Row:    ProductRow{Title: "新到貨", Href: "/search", Tiles: tiledShelf(4)},
		Band:   &DepartmentBand{Name: "館0", Href: "/c/d0", Tiles: tiledShelf(4)},
		Rules:  policy.Rules{FreeDeliveryCents: 300000},
	}))
	at := func(marker string) int { return strings.Index(page, marker) }
	order := []string{`class="goen-hero__tiles"`, `id="row-heading"`, `id="cats-heading"`, `id="band-heading"`, `id="rules-heading"`}
	previous := ""
	for _, marker := range order {
		if at(marker) < 0 {
			t.Errorf("the page draws no %s", marker)
		} else if previous != "" && at(marker) < at(previous) {
			t.Errorf("%s comes before %s; want %v", marker, previous, order)
		}
		previous = marker
	}
}
