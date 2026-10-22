package home

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/hero"
	"github.com/koopa0/goen/internal/ui/tone"
)

// The carousel draws one slide per entry, with a button only where the slide
// has one, and offers tabs and arrows only when there is something to move to.
func TestHeroCarouselDrawsItsSlides(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		see := i18n.T(ctx, i18n.KeyHeroCampaignCTA)
		campaign := hero.Slide{
			Layout: hero.LayoutPhoto, Tone: tone.Sage, Title: "Autumn desk sale",
			Stats: []components.Stat{{Label: "Items", Value: components.StatCount(4, "items")}},
			CTA:   hero.CTA{Label: see, Href: "/s/autumn-desk"},
			Photo: components.Photo{URL: "/static/a.webp", Alt: "a desk"}, PhotoWidth: 1600, PhotoHeight: 600,
		}
		department := hero.Slide{Layout: hero.LayoutSplit, Tone: tone.Mist, Title: "Tech", CTA: hero.CTA{Label: see, Href: "/c/tech"}}

		many := renderComponent(t, ctx, Page(Meta(ctx), View{Slides: []hero.Slide{campaign, department}}))
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

		one := renderComponent(t, ctx, Page(Meta(ctx), View{Slides: []hero.Slide{department}}))
		for _, banned := range []string{"goen-hero__controls", "goen-hero__tabs", "data-step", "carousel"} {
			if strings.Contains(one, banned) {
				t.Errorf("%s carousel of one slide draws %s", locale, banned)
			}
		}
		if strings.Contains(one, "goen-hero__lede") {
			t.Errorf("%s slide with no fact draws an empty line", locale)
		}

		none := renderComponent(t, ctx, Page(Meta(ctx), View{}))
		if strings.Contains(none, "goen-hero") {
			t.Errorf("%s home with no slides draws an empty carousel", locale)
		}
	}
}

// The carousel moves only when the visitor asks (WCAG 2.2.2 does not apply),
// so the markup holds no autoplay switch and no pause button.
func TestHeroCarouselHasNoAutoplay(t *testing.T) {
	t.Parallel()
	slides := []hero.Slide{
		{Layout: hero.LayoutSplit, Tone: tone.Mist, Title: "Tech"},
		{Layout: hero.LayoutSplit, Tone: tone.Sage, Title: "Food"},
	}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		got := renderComponent(t, ctx, Page(Meta(ctx), View{Slides: slides}))
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
	spec := components.PeriodSpec{Description: "秋日選物：10 月 1 日至 10 月 30 日，共 30 天", TodayLabel: "今天", Cells: []components.PeriodCell{{State: components.CellToday, Label: "10/9"}, {Label: "10/10"}}}
	slide := hero.Slide{
		Layout: hero.LayoutPhoto, Tone: tone.Sage, Title: "年終感謝祭全館滿額再折",
		Stats:  []components.Stat{{Label: "結束", Value: components.StatDate("10\u00a0月 30\u00a0日", ""), Note: "明天結束"}},
		Period: &spec,
	}
	got := renderComponent(t, ctx, Page(Meta(ctx), View{Slides: []hero.Slide{slide}}))
	for _, want := range []string{
		`<dt>結束</dt>`, `明天結束`, `class="ui-period" role="img" aria-label="秋日選物：10 月 1 日至 10 月 30 日，共 30 天"`,
		`<b>今天</b>`, `goen-hero__title goen-hero__title--long`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("campaign slide omits %s", want)
		}
	}
}
