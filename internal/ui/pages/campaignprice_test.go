package pages

import (
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
)

func runningCampaign(t *testing.T, locale i18n.Locale) ProductCampaign {
	t.Helper()
	now := time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC)
	endsAt := time.Date(2026, 10, 30, 16, 0, 0, 0, time.UTC)
	return NewProductCampaign("autumn-picks", titleIn(locale), endsAt, now)
}

func titleIn(locale i18n.Locale) string {
	if locale == i18n.En {
		return "Autumn picks"
	}
	return "秋日選物"
}

func TestAProductPageStrikesItsPriceOnlyWhileACampaignRuns(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		base := ProductView{
			Name: "Mug", SelectionOK: true, Exact: true, Sellable: true,
			PriceCents: 43200, CompareCents: 48000,
		}
		withCampaign := base
		withCampaign.Campaign = runningCampaign(t, locale)

		without := buyBox(t, locale, &base)
		if strings.Contains(without, `class="goen-pdp__was"`) || strings.Contains(without, `goen-pdp__source`) {
			t.Errorf("%s: a product outside any campaign draws a struck price or a source line:\n%s", locale, without)
		}

		with := buyBox(t, locale, &withCampaign)
		if !strings.Contains(with, `<s class="goen-pdp__was">NT$480</s>`) {
			t.Errorf("%s: a product in a running campaign lost its struck price", locale)
		}
		if strings.Contains(with, `class="ui-period"`) {
			t.Errorf("%s: the buy box draws its campaign's period; the source line says it in words", locale)
		}
	}
}

// The source line names the campaign, sets apart what is left of it and says its
// last day, in a sentence that fits how much is left.
func TestTheSourceLineNamesTheCampaignWhatIsLeftAndItsLastDay(t *testing.T) {
	t.Parallel()

	cst := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, cst)
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		ends   time.Time
		want   string
	}{
		{"days left", i18n.ZhHant, time.Date(2026, 10, 31, 0, 0, 0, 0, cst),
			"「<a href=\"/s/autumn-picks\">秋日選物</a>」活動價，<span class=\"goen-pdp__left\">剩\u00a021\u00a0天</span>，至 10\u00a0月 30\u00a0日</p>"},
		{"days left", i18n.En, time.Date(2026, 10, 31, 0, 0, 0, 0, cst),
			"<a href=\"/s/autumn-picks\">Autumn picks</a> price, <span class=\"goen-pdp__left\">21\u00a0days left</span>, until Oct\u00a030</p>"},
		{"tomorrow", i18n.ZhHant, time.Date(2026, 10, 11, 0, 0, 0, 0, cst),
			"「<a href=\"/s/autumn-picks\">秋日選物</a>」活動價，<span class=\"goen-pdp__left\">明天結束</span></p>"},
		{"tomorrow", i18n.En, time.Date(2026, 10, 11, 0, 0, 0, 0, cst),
			"<a href=\"/s/autumn-picks\">Autumn picks</a> price, <span class=\"goen-pdp__left\">ends tomorrow</span></p>"},
		{"today", i18n.ZhHant, time.Date(2026, 10, 10, 0, 0, 0, 0, cst),
			"「<a href=\"/s/autumn-picks\">秋日選物</a>」活動價，<span class=\"goen-pdp__left\">今天結束</span></p>"},
		{"today", i18n.En, time.Date(2026, 10, 10, 0, 0, 0, 0, cst),
			"<a href=\"/s/autumn-picks\">Autumn picks</a> price, <span class=\"goen-pdp__left\">ends today</span></p>"},
		{"days left at six", i18n.ZhHant, time.Date(2026, 10, 12, 18, 0, 0, 0, cst),
			"「<a href=\"/s/autumn-picks\">秋日選物</a>」活動價，<span class=\"goen-pdp__left\">剩\u00a03\u00a0天</span>，至 10\u00a0月 12\u00a0日 18:00</p>"},
		{"days left at six", i18n.En, time.Date(2026, 10, 12, 18, 0, 0, 0, cst),
			"<a href=\"/s/autumn-picks\">Autumn picks</a> price, <span class=\"goen-pdp__left\">3\u00a0days left</span>, until Oct\u00a012 at 18:00</p>"},
		{"tomorrow at six", i18n.ZhHant, time.Date(2026, 10, 10, 18, 0, 0, 0, cst),
			"「<a href=\"/s/autumn-picks\">秋日選物</a>」活動價，<span class=\"goen-pdp__left\">明天 18:00 結束</span></p>"},
		{"tomorrow at six", i18n.En, time.Date(2026, 10, 10, 18, 0, 0, 0, cst),
			"<a href=\"/s/autumn-picks\">Autumn picks</a> price, <span class=\"goen-pdp__left\">ends tomorrow at 18:00</span></p>"},
		{"today at six", i18n.ZhHant, time.Date(2026, 10, 9, 18, 0, 0, 0, cst),
			"「<a href=\"/s/autumn-picks\">秋日選物</a>」活動價，<span class=\"goen-pdp__left\">今天 18:00 結束</span></p>"},
		{"today at six", i18n.En, time.Date(2026, 10, 9, 18, 0, 0, 0, cst),
			"<a href=\"/s/autumn-picks\">Autumn picks</a> price, <span class=\"goen-pdp__left\">ends today at 18:00</span></p>"},
	} {
		v := ProductView{
			Name: "Mug", SelectionOK: true, Exact: true, Sellable: true,
			PriceCents: 43200, CompareCents: 48000, Campaign: NewProductCampaign("autumn-picks", titleIn(tt.locale), tt.ends, now),
		}
		if got := buyBox(t, tt.locale, &v); !strings.Contains(got, `<p class="goen-pdp__source">`+tt.want) {
			t.Errorf("%s, %s: want the source line %q in\n%s", tt.name, tt.locale, tt.want, got)
		}
	}
}

func TestACardStrikesItsPriceOnlyForACampaignProduct(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, tt := range []struct {
		name       string
		inCampaign bool
		wantStruck bool
	}{
		{"in a running campaign", true, true},
		{"no running campaign", false, false},
	} {
		tile := ProductTile{Slug: "a", Name: "A", Brand: "B", PriceCents: 80000, CompareCents: 100000, InStock: true, InCampaign: tt.inCampaign}
		page := renderComponent(t, ctx, Tile(tile))
		if got := strings.Contains(page, `class="goen-tile__was"`); got != tt.wantStruck {
			t.Errorf("Tile %s: struck price drawn = %v, want %v", tt.name, got, tt.wantStruck)
		}
		if strings.Contains(page, "原價") != tt.wantStruck {
			t.Errorf("Tile %s: the was-price label and the struck price disagree", tt.name)
		}
		if strings.Contains(page, "goen-tile__source") {
			t.Errorf("Tile %s: a card has no room for a source line", tt.name)
		}
	}
}

func TestThePickedVariantMustBeReducedForTheSourceLine(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		compare  int64
		sellable bool
	}{
		{"full price", 0, true},
		{"sold out", 48000, false},
	} {
		v := ProductView{
			Name: "Mug", SelectionOK: true, Exact: true, Sellable: tt.sellable,
			PriceCents: 43200, CompareCents: tt.compare, Campaign: runningCampaign(t, i18n.ZhHant),
		}
		got := buyBox(t, i18n.ZhHant, &v)
		if strings.Contains(got, "活動價") || strings.Contains(got, "goen-pdp__source") {
			t.Errorf("%s variant in a running campaign is called a campaign price", tt.name)
		}
	}
}
