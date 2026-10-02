package pages

import (
	"html"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestJoinListFollowsTheReadersLanguage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		locale i18n.Locale
		names  []string
		want   string
	}{
		{i18n.ZhHant, nil, ""},
		{i18n.ZhHant, []string{"書籍"}, "書籍"},
		{i18n.ZhHant, []string{"書籍", "文具"}, "書籍、文具"},
		{i18n.ZhHant, []string{"書籍", "文具", "食品"}, "書籍、文具、食品"},
		{i18n.En, []string{"Books"}, "Books"},
		{i18n.En, []string{"Books", "Stationery"}, "Books and Stationery"},
		{i18n.En, []string{"Books", "Stationery", "Food"}, "Books, Stationery and Food"},
	}
	for _, tt := range tests {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		if got := joinList(ctx, tt.names); got != tt.want {
			t.Errorf("%s joinList(%q) = %q, want %q", tt.locale, tt.names, got, tt.want)
		}
	}
}

// The home and about pages name the shop's departments from the header's
// category row, so a sentence about them cannot drift from the catalogue.
func TestHomeAndAboutNameTheShopsOwnCategories(t *testing.T) {
	t.Parallel()
	nav := map[i18n.Locale][]layouts.NavItem{
		i18n.ZhHant: {{Slug: "books", Name: "書籍"}, {Slug: "kitchen", Name: "廚房用品"}, {Slug: "food", Name: "食品"}},
		i18n.En:     {{Slug: "books", Name: "Books"}, {Slug: "kitchen", Name: "Kitchen"}, {Slug: "food", Name: "Food"}},
	}
	for locale, items := range nav {
		ctx := layouts.WithTopNav(i18n.WithLocale(t.Context(), locale), items)
		home, about := HomeMeta(ctx), AboutMeta(ctx)

		for _, item := range items {
			for name, got := range map[string]string{
				"home description":  home.Description,
				"about description": about.Description,
			} {
				if !strings.Contains(got, item.Name) {
					t.Errorf("%s %s %q does not name %s", locale, name, got, item.Name)
				}
			}
		}
		if home.Description == about.Description {
			t.Errorf("%s home and about share the description %q", locale, home.Description)
		}
		if home.Title != "" {
			t.Errorf("%s home title = %q, want none, so the tab reads as the shop's name", locale, home.Title)
		}

		names := make([]string, 0, len(items))
		for _, item := range items {
			names = append(names, item.Name)
		}
		list := joinList(ctx, names)
		page := renderComponent(t, ctx, About(about))
		if !strings.Contains(page, `<h1 class="about__title">`+i18n.T(ctx, i18n.KeyAboutTitle)+`</h1>`) {
			t.Errorf("%s about page's heading is not its title", locale)
		}
		if !strings.Contains(page, `<p class="about__valuebody">`+html.EscapeString(list)+`</p>`) {
			t.Errorf("%s about page does not list the categories %q", locale, list)
		}
	}
}

func TestHomeAndAboutSayNothingAboutCategoriesTheyCannotRead(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	if d := HomeMeta(ctx).Description; d != "" {
		t.Errorf("home description without categories = %q, want none", d)
	}
	if d := AboutMeta(ctx).Description; d != "" {
		t.Errorf("about description without categories = %q, want none", d)
	}

	home := renderComponent(t, ctx, Home(HomeMeta(ctx), HomeView{}))
	if !strings.Contains(home, "<title>"+i18n.T(ctx, i18n.KeySiteTitle)+"</title>") {
		t.Error("the home page's title is not the shop's name alone")
	}
	if strings.Contains(home, `<meta name="description"`) {
		t.Error("the home page states a description that names no category")
	}
	about := renderComponent(t, ctx, About(AboutMeta(ctx)))
	if strings.Contains(about, `<h2 class="about__valuetitle">`+i18n.T(ctx, i18n.KeyAboutCurated)+`</h2>`) {
		t.Error("the about page shows a categories card with nothing in it")
	}
}

// The carousel draws one slide per entry, with a button only where the slide
// has one, and offers arrows and dots only when there is something to move to.
func TestHeroCarouselDrawsItsSlides(t *testing.T) {
	t.Parallel()
	campaign := HeroSlide{
		Layout: SlidePhoto, Tone: ToneSage, Title: "Autumn desk sale",
		Fact:  "4 items · until 2027-10-01",
		CTA:   CTA{Label: "See the campaign", Href: "/s/autumn-desk"},
		Photo: Photo{URL: "/static/a.webp", Alt: "a desk"}, PhotoWidth: 1600, PhotoHeight: 600,
	}
	department := HeroSlide{Layout: SlideSplit, Tone: ToneMist, Title: "Tech", CTA: CTA{Label: "Browse Tech", Href: "/c/tech"}}

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)

		many := renderComponent(t, ctx, Home(HomeMeta(ctx), HomeView{Slides: []HeroSlide{campaign, department}}))
		for _, want := range []string{
			`class="goen-hero__slide goen-hero__slide--photo" data-tone="sage"`,
			`class="goen-hero__slide goen-hero__slide--split" data-tone="mist"`,
			`href="/s/autumn-desk"`, `href="/c/tech"`,
			`aria-label="1 / 2"`, `aria-label="2 / 2"`,
			`aria-label="` + i18n.T(ctx, i18n.KeyHeroNext) + `"`,
			`fetchpriority="high"`,
		} {
			if !strings.Contains(many, want) {
				t.Errorf("%s carousel omits %s", locale, want)
			}
		}
		if strings.Count(many, `fetchpriority="high"`) != 1 {
			t.Errorf("%s carousel prioritises more than the first photograph", locale)
		}
		if strings.Count(many, `class="goen-hero__dot"`) != 2 {
			t.Errorf("%s carousel has no dot per slide", locale)
		}

		one := renderComponent(t, ctx, Home(HomeMeta(ctx), HomeView{Slides: []HeroSlide{department}}))
		if strings.Contains(one, "goen-hero__controls") {
			t.Errorf("%s carousel of one slide draws arrows and dots", locale)
		}
		if strings.Contains(one, "goen-hero__lede") {
			t.Errorf("%s slide with no fact draws an empty line", locale)
		}

		none := renderComponent(t, ctx, Home(HomeMeta(ctx), HomeView{}))
		if strings.Contains(none, "goen-hero") {
			t.Errorf("%s home with no slides draws an empty carousel", locale)
		}
	}
}

// The struck-through original price already says a product is reduced, so a
// card carries no "on sale" chip; a sold-out one still carries its own.
func TestATileSaysSaleByItsPriceAndNotByAChip(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	sale := ProductTile{Slug: "a", Name: "A", Brand: "B", PriceCents: 80000, CompareCents: 100000, InStock: true}
	page := renderComponent(t, ctx, Tile(sale))
	if strings.Contains(page, i18n.T(ctx, i18n.KeyOnSale)) || strings.Contains(page, "goen-tile__flag") {
		t.Error("a reduced tile draws an on-sale chip")
	}
	if !strings.Contains(page, `class="goen-tile__was"`) {
		t.Error("a reduced tile lost its struck-through original price")
	}

	sold := sale
	sold.InStock = false
	if got := renderComponent(t, ctx, Tile(sold)); !strings.Contains(got, i18n.T(ctx, i18n.KeySoldOut)) {
		t.Error("a sold-out tile lost its chip")
	}
}

// Autoplay needs two or more slides, and the pause control that WCAG 2.2.2
// asks for travels with it: hidden until goen.js starts the carousel, labelled
// for both states in the reader's language.
func TestHeroCarouselOffersAPauseControlOnlyWhenItMoves(t *testing.T) {
	t.Parallel()
	one := []HeroSlide{{Layout: SlideSplit, Tone: ToneMist, Title: "Tech"}}
	two := []HeroSlide{one[0], {Layout: SlideSplit, Tone: ToneSage, Title: "Food"}}

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)

		moving := renderComponent(t, ctx, Home(HomeMeta(ctx), HomeView{Slides: two}))
		for _, want := range []string{
			`data-autoplay`,
			`class="goen-hero__pause" type="button" hidden`,
			`data-label-pause="` + i18n.T(ctx, i18n.KeyHeroPause) + `"`,
			`data-label-play="` + i18n.T(ctx, i18n.KeyHeroPlay) + `"`,
		} {
			if !strings.Contains(moving, want) {
				t.Errorf("%s carousel of two omits %s", locale, want)
			}
		}

		still := renderComponent(t, ctx, Home(HomeMeta(ctx), HomeView{Slides: one}))
		if strings.Contains(still, "data-autoplay") || strings.Contains(still, "goen-hero__pause") {
			t.Errorf("%s carousel of one offers autoplay or a pause control", locale)
		}
	}
	if i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyHeroPause) ==
		i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyHeroPlay) {
		t.Error("the pause and play labels read the same")
	}
}
