package pages

import (
	"html"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
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
		wantTitle := map[i18n.Locale]string{i18n.ZhHant: "館別", i18n.En: "Departments"}[locale]
		if !strings.Contains(page, `<h2 class="about__valuetitle">`+wantTitle+`</h2>`) {
			t.Errorf("%s about page does not head its department list %q", locale, wantTitle)
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
// has one, and offers tabs and arrows only when there is something to move to.
func TestHeroCarouselDrawsItsSlides(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		see := i18n.T(ctx, i18n.KeyHeroCampaignCTA)
		campaign := HeroSlide{
			Layout: SlidePhoto, Tone: ToneSage, Title: "Autumn desk sale",
			Stats: []components.Stat{{Label: "Items", Value: components.StatCount(4, "items")}},
			CTA:   CTA{Label: see, Href: "/s/autumn-desk"},
			Photo: Photo{URL: "/static/a.webp", Alt: "a desk"}, PhotoWidth: 1600, PhotoHeight: 600,
		}
		department := HeroSlide{Layout: SlideSplit, Tone: ToneMist, Title: "Tech", CTA: CTA{Label: see, Href: "/c/tech"}}

		many := renderComponent(t, ctx, Home(HomeMeta(ctx), HomeView{Slides: []HeroSlide{campaign, department}}))
		for _, want := range []string{
			`class="goen-hero__slide goen-hero__slide--photo" data-tone="sage"`,
			`class="goen-hero__slide goen-hero__slide--split goen-hero__slide--text" data-tone="mist"`,
			`href="/s/autumn-desk"`, `href="/c/tech"`,
			`aria-label="1 / 2"`, `aria-label="2 / 2"`,
			`aria-label="` + i18n.T(ctx, i18n.KeyHeroNext) + `"`,
			`href="#hero-2"`,
			`fetchpriority="high"`,
		} {
			if !strings.Contains(many, want) {
				t.Errorf("%s carousel omits %s", locale, want)
			}
		}
		if strings.Count(many, `fetchpriority="high"`) != 1 {
			t.Errorf("%s carousel prioritises more than the first photograph", locale)
		}
		if strings.Count(many, `aria-current="true"`) != 1 {
			t.Errorf("%s carousel marks %d tabs as current, want 1", locale, strings.Count(many, `aria-current="true"`))
		}
		if strings.Count(many, `data-js hidden`) != 1 {
			t.Errorf("%s carousel does not leave its arrows to the script", locale)
		}
		if strings.Count(many, i18n.T(ctx, i18n.KeyHeroCampaignCTA)) != 2 {
			t.Errorf("%s carousel does not link both slide kinds with the one label", locale)
		}

		one := renderComponent(t, ctx, Home(HomeMeta(ctx), HomeView{Slides: []HeroSlide{department}}))
		for _, banned := range []string{"goen-hero__controls", "goen-hero__tabs", "data-step", "carousel"} {
			if strings.Contains(one, banned) {
				t.Errorf("%s carousel of one slide draws %s", locale, banned)
			}
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
// card carries no "on sale" chip.
func TestATileSaysSaleByItsPriceAndNotByAChip(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	sale := ProductTile{Slug: "a", Name: "A", Brand: "B", PriceCents: 80000, CompareCents: 100000, InStock: true, InCampaign: true}
	page := renderComponent(t, ctx, Tile(sale))
	if strings.Contains(page, i18n.T(ctx, i18n.KeyOnSale)) {
		t.Error("a reduced tile draws an on-sale chip")
	}
	if !strings.Contains(page, `class="goen-tile__was"`) {
		t.Error("a reduced tile lost its struck-through original price")
	}
}

// The carousel moves only when the visitor asks (WCAG 2.2.2 does not apply),
// so the markup holds no autoplay switch and no pause button.
func TestHeroCarouselHasNoAutoplay(t *testing.T) {
	t.Parallel()
	slides := []HeroSlide{
		{Layout: SlideSplit, Tone: ToneMist, Title: "Tech"},
		{Layout: SlideSplit, Tone: ToneSage, Title: "Food"},
	}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		got := renderComponent(t, ctx, Home(HomeMeta(ctx), HomeView{Slides: slides}))
		for _, banned := range []string{"data-autoplay", "goen-hero__pause"} {
			if strings.Contains(got, banned) {
				t.Errorf("%s carousel draws %s", locale, banned)
			}
		}
		for _, want := range []string{`data-step="-1"`, `data-step="1"`} {
			if !strings.Contains(got, want) {
				t.Errorf("%s carousel omits %s", locale, want)
			}
		}
	}
}

// A campaign slide draws its facts and its grid; a long title steps down.
func TestACampaignSlideDrawsItsStatsAndItsDayGrid(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	spec := components.PeriodSpec{Description: "秋日選物：10 月 1 日至 10 月 30 日，共 30 天", TodayLabel: "今天", Cells: []components.PeriodCell{{State: components.CellToday, Date: "10/9"}, {Date: "10/10"}}}
	slide := HeroSlide{
		Layout: SlidePhoto, Tone: ToneSage, Title: "年終感謝祭全館滿額再折",
		Stats:  []components.Stat{{Label: "結束", Value: components.StatDate("10\u00a0月 30\u00a0日", ""), Note: "明天結束"}},
		Period: &spec,
	}
	got := renderComponent(t, ctx, Home(HomeMeta(ctx), HomeView{Slides: []HeroSlide{slide}}))
	for _, want := range []string{
		`<dt>結束</dt>`, `明天結束`, `class="ui-period" role="img" aria-label="秋日選物：10 月 1 日至 10 月 30 日，共 30 天"`,
		`<b>今天</b>`, `goen-hero__title goen-hero__title--long`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("campaign slide omits %s", want)
		}
	}
}
