package home

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/policy"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/hero"
	"github.com/koopa0/goen/internal/ui/tone"
	"github.com/koopa0/goen/internal/ui/widget"
)

// The carousel draws one slide per entry, with a button only where the slide
// has one, and offers tabs and arrows only when there is something to move to.
func TestHeroCarouselDrawsItsSlides(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		see := i18n.T(ctx, i18n.KeyHeroCampaignCTA)
		running := hero.Slide{
			Layout: hero.LayoutPhoto, Tone: tone.Sage, Title: "Autumn desk sale",
			Ends:  campaign.Ends(campaign.NewEnd(time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC))),
			CTA:   hero.CTA{Label: see, Href: "/s/autumn-desk"},
			Photo: widget.Photo{URL: "/static/a.webp", Alt: "a desk"}, PhotoWidth: 1600, PhotoHeight: 600,
		}
		department := hero.Slide{Layout: hero.LayoutSplit, Tone: tone.Mist, Title: "Tech", CTA: hero.CTA{Label: see, Href: "/c/tech"}}

		many := renderComponent(t, ctx, Page(Meta(ctx), View{Slides: []hero.Slide{running, department}}))
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
		if strings.Contains(many, "aria-current") {
			t.Errorf("%s carousel marks a tab as current before the script has run", locale)
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

// A slide with no photograph draws no field for one, so no slide is left
// holding an empty slab; the hero stands its tabs over the slides then, where
// each begins, and keeps them at the photographs' foot when every slide has one.
func TestHeroCarouselWithoutPhotograph(t *testing.T) {
	t.Parallel()
	photographed := hero.Slide{Layout: hero.LayoutPhoto, Tone: tone.Sage, Title: "A", Photo: widget.Photo{URL: "/static/a.webp", Alt: "a"}, PhotoWidth: 1600, PhotoHeight: 600}
	bare := hero.Slide{Layout: hero.LayoutSplit, Tone: tone.Mist, Title: "B"}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		mixed := renderComponent(t, ctx, Page(Meta(ctx), View{Slides: []hero.Slide{photographed, bare}}))
		if !strings.Contains(mixed, `class="goen-hero goen-hero--bare"`) {
			t.Errorf("%s: a hero with a slide that has no photograph is not marked bare", locale)
		}
		if got := strings.Count(mixed, `class="goen-hero__media"`); got != 1 {
			t.Errorf("%s: %d photograph fields for one photograph", locale, got)
		}
		all := renderComponent(t, ctx, Page(Meta(ctx), View{Slides: []hero.Slide{photographed, photographed}}))
		if strings.Contains(all, "goen-hero--bare") {
			t.Errorf("%s: a hero whose slides all have a photograph is marked bare", locale)
		}
		if got := strings.Count(all, `class="goen-hero__media"`); got != 2 {
			t.Errorf("%s: %d photograph fields for two photographs", locale, got)
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

// TestHeroCarouselCampaignSlide: A campaign slide says its end in one phrase and
// draws its days beside it as a track a screen reader skips, one cell a day and
// the days gone marked; a long title steps down.
func TestHeroCarouselCampaignSlide(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	spec := widget.PeriodSpec{Description: "秋日選物：10 月 1 日至 10 月 30 日，共 30 天", TodayLabel: "今天", Cells: []widget.PeriodCell{
		{State: widget.CellPast, Label: "10/7"}, {State: widget.CellPast}, {State: widget.CellToday}, {}, {Label: "10/11"},
	}}
	slide := hero.Slide{
		Layout: hero.LayoutPhoto, Tone: tone.Sage, Title: "年終感謝祭全館滿額再折",
		Ends:   campaign.Ends(campaign.NewEnd(time.Date(2026, 10, 11, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)), time.Date(2026, 10, 9, 12, 0, 0, 0, time.FixedZone("CST", 8*3600)))),
		Period: &spec,
	}
	got := renderComponent(t, ctx, Page(Meta(ctx), View{Slides: []hero.Slide{slide}}))
	for _, want := range []string{
		`<p class="goen-hero__ends">`, `明天結束`, `goen-hero__title goen-hero__title--long`,
		`<div class="goen-days" aria-hidden="true"><i data-cell="past"></i><i data-cell="past"></i><i></i><i></i><i></i></div>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("campaign slide omits %s", want)
		}
	}
	// The words beside the track say the end; the track repeats none of them.
	for _, banned := range []string{"ui-period", spec.Description, "<b>今天</b>", "10/11"} {
		if strings.Contains(got, banned) {
			t.Errorf("campaign slide draws %s", banned)
		}
	}
}

// TestHeroCarouselCarriesItsProducts holds that a campaign slide draws its own
// products under its name, every slide's loaded at once (a slide beside the
// window is in a scroller, where a lazy photograph may never be asked for), and
// that the home page has no filled button: a
// slide's way on is a link, as every section's is.
func TestHeroCarouselCarriesItsProducts(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		see := i18n.T(ctx, i18n.KeyHeroCampaignCTA)
		slides := []hero.Slide{
			{Source: hero.SourceCampaign, Title: "Tea", CTA: hero.CTA{Label: see, Href: "/s/tea"}, Tiles: tiledShelf(4)},
			{Source: hero.SourceCampaign, Title: "Autumn", CTA: hero.CTA{Label: see, Href: "/s/autumn"}, Tiles: tiledShelf(4)},
			{Source: hero.SourceScheduled, Title: "Editor", CTA: hero.CTA{Label: "Read on", Href: "/about"}},
		}
		page := renderComponent(t, frame.WithTopNav(ctx, departmentsOf(2)), Page(Meta(ctx), View{
			Slides: slides,
			Row:    ProductRow{Title: "Tea", Href: "/s/tea", Tiles: tiledShelf(4), Campaign: &RowCampaign{Items: 4}},
			Band:   &DepartmentBand{Name: "Home", Href: "/c/home", Items: 12, Tiles: tiledShelf(4)},
			Rules:  policy.Rules{FreeDeliveryCents: 300000},
		}))
		if strings.Contains(page, `id="row-heading"`) {
			t.Errorf("%s: the row repeats the products its campaign's slide carries", locale)
		}
		if got := strings.Count(page, `<ul class="goen-hero__tiles" role="list">`); got != 2 {
			t.Errorf("%s: %d slides draw products, want the two campaigns", locale, got)
		}
		first := page[strings.Index(page, `id="hero-1"`):strings.Index(page, `id="hero-2"`)]
		second := page[strings.Index(page, `id="hero-2"`):strings.Index(page, `id="hero-3"`)]
		for name, slide := range map[string]string{"first": first, "second": second} {
			if n := strings.Count(slide, `loading="lazy"`); n != 0 {
				t.Errorf("%s: %d of the %s slide's products wait to load", locale, n, name)
			}
		}
		// The footer's subscribe is a button of its own, outside the page's main.
		if strings.Contains(page, "goen-btn--primary") {
			t.Errorf("%s: the home page draws a filled button", locale)
		}
		main := page[strings.Index(page, "<main"):strings.Index(page, "</main>")]
		for _, banned := range []string{"goen-btn", "ui-btn", "goen-hero__actions"} {
			if strings.Contains(main, banned) {
				t.Errorf("%s: the home page draws %s; its ways on are links", locale, banned)
			}
		}
		for _, href := range []string{"/s/tea", "/s/autumn", "/about"} {
			if !strings.Contains(page, `<a class="goen-home__more" href="`+href+`">`) {
				t.Errorf("%s: the slide linking %s has no next-step link", locale, href)
			}
		}
		for i := range slides {
			if want := fmt.Sprintf(`<span class="goen-hero__no" aria-hidden="true">%02d</span>`, i+1); !strings.Contains(page, want) {
				t.Errorf("%s: the campaign index lacks %s", locale, want)
			}
		}
	}
}

// TestHeroCarriesOnlyItsOwnRow holds that the row under the hero is left out
// only when a campaign slide draws that campaign's products.
func TestHeroCarriesOnlyItsOwnRow(t *testing.T) {
	t.Parallel()
	row := ProductRow{Href: "/s/tea", Tiles: tiledShelf(4), Campaign: &RowCampaign{Items: 4}}
	tea := hero.Slide{Source: hero.SourceCampaign, CTA: hero.CTA{Href: "/s/tea"}, Tiles: tiledShelf(4)}
	for name, tt := range map[string]struct {
		slides []hero.Slide
		row    ProductRow
		want   bool
	}{
		"the campaign's slide":             {[]hero.Slide{tea}, row, true},
		"another campaign's slide":         {[]hero.Slide{{Source: hero.SourceCampaign, CTA: hero.CTA{Href: "/s/autumn"}, Tiles: tiledShelf(4)}}, row, false},
		"its slide without products":       {[]hero.Slide{{Source: hero.SourceCampaign, CTA: hero.CTA{Href: "/s/tea"}}}, row, false},
		"a scheduled slide linking to it":  {[]hero.Slide{{Source: hero.SourceScheduled, CTA: hero.CTA{Href: "/s/tea"}, Tiles: tiledShelf(4)}}, row, false},
		"the newest products, no campaign": {[]hero.Slide{tea}, ProductRow{Href: "/search", Tiles: tiledShelf(4)}, false},
	} {
		if got := heroCarries(tt.slides, tt.row); got != tt.want {
			t.Errorf("%s: heroCarries = %t, want %t", name, got, tt.want)
		}
	}
}
