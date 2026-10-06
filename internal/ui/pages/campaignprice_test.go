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
	startsAt := time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)
	endsAt := time.Date(2026, 10, 30, 16, 0, 0, 0, time.UTC)
	return NewProductCampaign(i18n.WithLocale(t.Context(), locale), "autumn-picks", titleIn(locale), startsAt, endsAt, now)
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
		if !strings.Contains(with, `class="ui-period"`) {
			t.Errorf("%s: a product in a running campaign has no day grid", locale)
		}
	}
}

func TestTheSourceLineNamesTheCampaignAndItsLastDay(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		locale i18n.Locale
		want   string
	}{
		{i18n.ZhHant, "<p class=\"goen-pdp__source\"><a href=\"/s/autumn-picks\">秋日選物</a>活動價，至 10\u00a0月 30\u00a0日</p>"},
		{i18n.En, "<p class=\"goen-pdp__source\"><a href=\"/s/autumn-picks\">Autumn picks</a> price, until Oct\u00a030</p>"},
	} {
		v := ProductView{
			Name: "Mug", SelectionOK: true, Exact: true, Sellable: true,
			PriceCents: 43200, CompareCents: 48000, Campaign: runningCampaign(t, tt.locale),
		}
		if got := buyBox(t, tt.locale, &v); !strings.Contains(got, tt.want) {
			t.Errorf("%s source line: want %q in\n%s", tt.locale, tt.want, got)
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
