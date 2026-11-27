package home

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/policy"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/hero"
	"github.com/koopa0/goen/internal/ui/tile"
)

// TestMetaLinkPreviewTags holds that the home page sends its own link-preview
// tags. The home page's row of internal/contact's TestMetaLinkPreviewTags,
// which lives here since the page does.
func TestMetaLinkPreviewTags(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(frame.WithRequestPath(t.Context(), "/c/books"), i18n.ZhHant)
	head := renderComponent(t, frame.WithSiteOrigin(ctx, "https://goen.test"), frame.Base(Meta(ctx)))
	for _, want := range []string{
		`og:title" content="` + i18n.T(ctx, i18n.KeySiteTitle) + `"`,
		`og:type" content="website"`,
		`og:site_name" content="goen"`,
		`og:url" content="https://goen.test/c/books"`,
		`name="twitter:card" content="summary_large_image"`,
	} {
		if !strings.Contains(head, want) {
			t.Errorf("home head omits %s", want)
		}
	}
}

// TestPageShopRules holds that the home page ends with the shop rules it is
// given, four cards under a heading. What each rule says is policy's
// TestRulesSectionStoredRules.
func TestPageShopRules(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	page := renderComponent(t, ctx, Page(frame.Page{}, View{Rules: policy.Rules{FreeDeliveryCents: 300000}}))
	if !strings.Contains(page, `<section class="goen-home__band goen-home__rules" aria-labelledby="rules-heading"><div class="goen-home"><h2 id="rules-heading" class="goen-home__heading">`+i18n.T(ctx, i18n.KeySectionRules)+`</h2>`) {
		t.Error("home page lacks the shop rules under their heading")
	}
	if n := strings.Count(page, "<dt>"); n != 4 {
		t.Errorf("home page states %d rules, want 4", n)
	}
}

func someTiles(n int) []tile.Tile {
	out := make([]tile.Tile, n)
	for i := range out {
		out[i] = tile.Tile{Slug: "p" + string(rune('a'+i)), Name: "P", PriceCents: 100000 * int64(i+1), InStock: true,
			ImageURL: "/img/p.webp", ImageWidth: 1600, ImageHeight: 1200}
	}
	return out
}

func fullView() View {
	tech := Category{Slug: "tech", Name: "3C 數位", Products: 20}
	return View{
		Products:  40,
		Featured:  &tech,
		Lifestyle: &hero.Slide{Source: hero.SourceDepartment, Title: "居家生活", CTA: hero.CTA{Label: "居家生活", Href: "/c/home-living"}},
		Shelf: &Shelf{Department: tech, Filters: []Category{
			{Slug: "phones", Name: "手機", Products: 3}, {Slug: "laptops", Name: "筆電", Products: 3},
		}, Chosen: "phones", Tiles: someTiles(3)},
		Stage: someTiles(2),
		Rail:  Rail{BelowCents: 100000, Tiles: someTiles(5)},
		More: &More{
			Tabs:   []Category{{Slug: "books", Name: "書籍文具", Products: 4}, {Slug: "home-living", Name: "居家生活", Products: 4}},
			Chosen: Category{Slug: "books", Name: "書籍文具", Products: 4},
			Tiles:  someTiles(4),
		},
		Choice: Choice{Shelf: "phones", More: "books"},
		Rules:  policy.Rules{FreeDeliveryCents: 300000, LowestFeeCents: 6000, RescissionDays: 7, ReturnDays: 14, PayStartMinutes: 29},
	}
}

// TestPageSections holds the page's five rhythms in their order under the
// one heading: the hero, the shelf, the stage, the rail and the showcase, then
// the rules; and that each part's pills are links to the page's own address
// with the choice, which htmx fetches to take only that part.
func TestPageSections(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	page := renderComponent(t, ctx, Page(frame.Page{}, fullView()))
	at := -1
	for _, want := range []string{
		`<section class="goen-bento"`, `<section class="goen-home__band" id="shelf"`, `<section class="goen-stage"`,
		`<section class="goen-home__band" id="under"`, `<section class="goen-home__band" id="more"`, `goen-home__rules"`,
	} {
		i := strings.Index(page, want)
		if i < at {
			t.Errorf("%s is missing or out of order", want)
		}
		at = max(at, i)
	}
	if n := strings.Count(page, "<h1"); n != 1 || !strings.Contains(page, `<h1 id="hero-heading" class="goen-bento__headline">`+i18n.T(ctx, i18n.KeyHeroHeadline)+`</h1>`) {
		t.Errorf("the page has %d headings of the first rank, want the hero's headline alone", n)
	}
	for _, pill := range []struct{ id, href, target string }{
		{"shelf-all", "/?more=books", "shelf"},
		{"shelf-laptops", "/?more=books&amp;shelf=laptops", "shelf"},
		{"more-home-living", "/?more=home-living&amp;shelf=phones", "more"},
	} {
		want := `id="` + pill.id + `" class="goen-pill" href="` + pill.href + `" hx-get="` + pill.href + `" hx-target="#` + pill.target + `" hx-select="#` + pill.target + `"`
		if !strings.Contains(page, want) {
			t.Errorf("the %s pill is not a link htmx fetches from its own address: want %s", pill.id, want)
		}
	}
	for _, current := range []string{`id="shelf-phones" class="goen-pill" href="/?more=books&amp;shelf=phones" aria-current="true"`, `id="more-books" class="goen-pill" href="/?more=books&amp;shelf=phones" aria-current="true"`} {
		if !strings.Contains(page, current) {
			t.Errorf("the chosen pill is not marked current: want %s", current)
		}
	}
	if n := strings.Count(page, `aria-current="true"`); n != 2 {
		t.Errorf("%d pills are current, want the one of each row", n)
	}
}

// TestPageWithoutProducts holds that a shop with no products draws the hero
// and the rules and nothing that would stand empty.
func TestPageWithoutProducts(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	page := renderComponent(t, ctx, Page(frame.Page{}, View{}))
	for _, absent := range []string{`id="shelf"`, `goen-stage`, `id="under"`, `id="more"`, `goen-bento__life`, `goen-bento__lede`, `goen-bento__cta`} {
		if strings.Contains(page, absent) {
			t.Errorf("a shop with no products draws %s", absent)
		}
	}
	if !strings.Contains(page, `class="goen-bento__headline"`) || !strings.Contains(page, `goen-home__rules`) {
		t.Error("a shop with no products lost its hero or its rules")
	}
}

// TestPageHeroCount holds that the hero counts the products the shop has now,
// in the department's eyebrow, the lede and the way in, in both languages.
func TestPageHeroCount(t *testing.T) {
	t.Parallel()
	view := fullView()
	view.Products, view.Featured.Products = 37, 19
	for locale, want := range map[i18n.Locale][]string{
		i18n.ZhHant: {"3C 數位 · 19 件商品", "全館 37 件商品", "探索 3C 數位（19）"},
		i18n.En:     {"3C 數位 · 19 products", "37 products, their", "Shop 3C 數位 (19)"},
	} {
		page := renderIn(t, locale, Page(frame.Page{}, view))
		for _, w := range want {
			if !strings.Contains(page, w) {
				t.Errorf("%s hero lacks %q", locale, w)
			}
		}
	}
}

// TestChoice_Href holds that a choice is an address of the home page that a
// reload keeps, and none is the page itself.
func TestChoice_Href(t *testing.T) {
	t.Parallel()
	for c, want := range map[Choice]string{
		{}:                             "/",
		{Shelf: "phones"}:              "/?shelf=phones",
		{More: "books"}:                "/?more=books",
		{Shelf: "phones", More: "tea"}: "/?more=tea&shelf=phones",
	} {
		if got := c.Href(); got != want {
			t.Errorf("%+v.Href() = %q, want %q", c, got, want)
		}
	}
}

// TestSlugParam holds that only a slug's characters reach the store from the
// address; anything else is the default.
func TestSlugParam(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"phones": "phones", "home-living": "home-living", "a1": "a1",
		"": "", "Phones": "", "../x": "", "a b": "", "手機": "", strings.Repeat("a", 121): "",
	} {
		if got := slugParam(in); got != want {
			t.Errorf("slugParam(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestView_Index holds the quick index: the featured department's
// sub-categories in its order, then the rail and the deals where each has
// something, which take the last of the four places.
func TestView_Index(t *testing.T) {
	t.Parallel()
	subs := []Category{{Slug: "a", Name: "A"}, {Slug: "b", Name: "B"}, {Slug: "c", Name: "C"}, {Slug: "d", Name: "D"}, {Slug: "e", Name: "E"}}
	base := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, tc := range []struct {
		name  string
		rail  bool
		deals bool
		want  []string
	}{
		{"subs alone", false, false, []string{"/c/a", "/c/b", "/c/c", "/c/d"}},
		{"with the rail", true, false, []string{"/c/a", "/c/b", "/c/c", "#under"}},
		{"with deals", false, true, []string{"/c/a", "/c/b", "/c/c", "/deals"}},
		{"with both", true, true, []string{"/c/a", "/c/b", "#under", "/deals"}},
	} {
		v := View{Shelf: &Shelf{Filters: subs}}
		if tc.rail {
			v.Rail = Rail{BelowCents: 100000, Tiles: someTiles(1)}
		}
		ctx := frame.WithDeals(base, tc.deals)
		var got []string
		for _, l := range v.Index(ctx) {
			got = append(got, l.Href)
		}
		if strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("%s: Index() = %v, want %v", tc.name, got, tc.want)
		}
	}
	v := View{Rail: Rail{BelowCents: 100000, Tiles: someTiles(1)}}
	if links := v.Index(base); len(links) != 1 || links[0].Label != "未滿 NT$1,000" {
		t.Errorf("Index() with no shelf = %+v, want the rail alone, named by its ceiling", links)
	}
}

var railWord = regexp.MustCompile(`<a class="goen-rail__delivery" href="([^"]*)">([^<]*)</a>`)

// TestCartMeter holds that the meter says what the header's rail says, in the
// same cases, with the bar where there is a figure to fill: the rail and the
// meter are the same claim and may not disagree.
func TestCartMeter(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		cart       frame.CartSummary
		over       int64
		value, max int64
		free       bool
	}{
		{name: "empty cart, a threshold", over: 300000, max: 300000},
		{name: "short", cart: frame.CartSummary{Units: 2, SubtotalCents: 190700, Delivery: frame.CartDelivery{ShortCents: 109300}}, over: 300000, value: 190700, max: 300000},
		{name: "free", cart: frame.CartSummary{Units: 3, SubtotalCents: 320000, Delivery: frame.CartDelivery{Free: true}}, over: 300000, value: 320000, max: 320000, free: true},
		{name: "free but islands", cart: frame.CartSummary{Units: 3, SubtotalCents: 320000, Delivery: frame.CartDelivery{Free: true, Zones: []string{"澎湖", "金門"}}}, over: 300000, value: 320000, max: 320000, free: true},
		{name: "methods disagree", cart: frame.CartSummary{Units: 1, SubtotalCents: 50000}, over: 300000},
		{name: "no threshold", over: 0},
	} {
		for _, locale := range i18n.Locales() {
			ctx := frame.WithFreeDeliveryOver(frame.WithCart(i18n.WithLocale(t.Context(), locale), tc.cart), tc.over)
			m := CartMeter(ctx)
			header := renderComponent(t, ctx, frame.Header(frame.Page{}))
			word := railWord.FindStringSubmatch(header)
			if word == nil {
				if m.Shown() {
					t.Errorf("%s %s: the meter says %q where the rail says nothing", tc.name, locale, m.Sentence)
				}
				continue
			}
			if m.Sentence != htmlText(word[2]) || m.Href != word[1] {
				t.Errorf("%s %s: the meter says %q to %s, the rail %q to %s", tc.name, locale, m.Sentence, m.Href, htmlText(word[2]), word[1])
			}
			if m.Value != tc.value || m.Max != tc.max || m.Free != tc.free {
				t.Errorf("%s %s: the bar is %d of %d free %t, want %d of %d free %t", tc.name, locale, m.Value, m.Max, m.Free, tc.value, tc.max, tc.free)
			}
		}
	}
}

func htmlText(s string) string {
	return strings.NewReplacer("&amp;", "&", "&#39;", "'", "&quot;", `"`).Replace(s)
}

// TestPageMeterFigure holds that the meter's figure says the subtotal over the
// amount that makes delivery free only while the cart is short of it.
func TestPageMeterFigure(t *testing.T) {
	t.Parallel()
	view := View{Rail: Rail{BelowCents: 100000, Tiles: someTiles(1)}}
	render := func(ctx context.Context) string { return renderComponent(t, ctx, Page(frame.Page{}, view)) }
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	short := render(frame.WithCart(ctx, frame.CartSummary{Units: 1, SubtotalCents: 190700, Delivery: frame.CartDelivery{ShortCents: 109300}}))
	if !strings.Contains(short, `<progress class="goen-meter__bar" value="190700" max="300000" aria-labelledby="meter-text"></progress>`) ||
		!strings.Contains(short, `<span class="goen-meter__figure" aria-hidden="true">NT$1,907 / NT$3,000</span>`) {
		t.Error("a cart short of free delivery has no bar or figure of how far")
	}
	free := render(frame.WithCart(ctx, frame.CartSummary{Units: 1, SubtotalCents: 320000, Delivery: frame.CartDelivery{Free: true}}))
	if !strings.Contains(free, `goen-meter__bar goen-meter__bar--free`) || strings.Contains(free, "goen-meter__figure") {
		t.Error("a cart delivered free draws no full bar, or still counts towards it")
	}
	if quiet := render(frame.WithCart(ctx, frame.CartSummary{Units: 1, SubtotalCents: 50000})); strings.Contains(quiet, "goen-meter__text") {
		t.Error("the meter speaks where the cart's page says nothing")
	}
}
