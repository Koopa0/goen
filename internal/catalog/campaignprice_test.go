package catalog

import (
	"regexp"
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

// TestProductStrikesPriceOnSale holds that the product page strikes its price
// and names the campaign only on sale.
//
// Whether a price is on sale is campaign.OnSale's to decide; the page strikes
// the price and names the campaign only when it is.
func TestProductStrikesPriceOnSale(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		base := ProductView{
			Name: "Mug", SelectionOK: true, Exact: true, Sellable: true,
			PriceCents: 43200, CompareCents: 48000, Campaign: runningCampaign(t, locale),
		}
		withCampaign := base
		withCampaign.OnSale = true

		without := buyBox(t, locale, &base)
		if strings.Contains(without, `class="goen-pdp__was"`) || strings.Contains(without, `goen-pdp__source`) {
			t.Errorf("%s: a product not on sale draws a struck price or a source line:\n%s", locale, without)
		}

		with := buyBox(t, locale, &withCampaign)
		if !strings.Contains(with, `<s class="goen-pdp__was">NT$480</s>`) {
			t.Errorf("%s: a product on sale lost its struck price", locale)
		}
		if strings.Contains(with, `class="ui-period"`) {
			t.Errorf("%s: the buy box draws its campaign's period; the source line says it in words", locale)
		}
	}
}

// TestProductCampaignSource holds that the source line names the campaign and
// then says its end the way every other page does: the last day first, then
// what is left, and the last two days whole.
func TestProductCampaignSource(t *testing.T) {
	t.Parallel()

	cst := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, cst)
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		ends   time.Time
		want   string
	}{
		{"days left", i18n.ZhHant, time.Date(2026, 10, 31, 0, 0, 0, 0, cst), "「秋日選物」活動價\u00a0· 至 10\u00a0月 30\u00a0日\u00a0· 剩\u00a021\u00a0天"},
		{"days left", i18n.En, time.Date(2026, 10, 31, 0, 0, 0, 0, cst), "Autumn picks price\u00a0· until Oct\u00a030,\u00a0· 21\u00a0days left"},
		{"tomorrow", i18n.ZhHant, time.Date(2026, 10, 11, 0, 0, 0, 0, cst), "「秋日選物」活動價\u00a0· 明天結束"},
		{"tomorrow", i18n.En, time.Date(2026, 10, 11, 0, 0, 0, 0, cst), "Autumn picks price\u00a0· ends tomorrow"},
		{"today", i18n.ZhHant, time.Date(2026, 10, 10, 0, 0, 0, 0, cst), "「秋日選物」活動價\u00a0· 今天結束"},
		{"today", i18n.En, time.Date(2026, 10, 10, 0, 0, 0, 0, cst), "Autumn picks price\u00a0· ends today"},
		{"days left at six", i18n.ZhHant, time.Date(2026, 10, 12, 18, 0, 0, 0, cst), "「秋日選物」活動價\u00a0· 至 10\u00a0月 12\u00a0日 18:00\u00a0· 剩\u00a03\u00a0天"},
		{"days left at six", i18n.En, time.Date(2026, 10, 12, 18, 0, 0, 0, cst), "Autumn picks price\u00a0· until Oct\u00a012 at 18:00,\u00a0· 3\u00a0days left"},
		{"tomorrow at six", i18n.ZhHant, time.Date(2026, 10, 10, 18, 0, 0, 0, cst), "「秋日選物」活動價\u00a0· 明天 18:00 結束"},
		{"tomorrow at six", i18n.En, time.Date(2026, 10, 10, 18, 0, 0, 0, cst), "Autumn picks price\u00a0· ends tomorrow at 18:00"},
		{"today at six", i18n.ZhHant, time.Date(2026, 10, 9, 18, 0, 0, 0, cst), "「秋日選物」活動價\u00a0· 今天 18:00 結束"},
		{"today at six", i18n.En, time.Date(2026, 10, 9, 18, 0, 0, 0, cst), "Autumn picks price\u00a0· ends today at 18:00"},
	} {
		v := ProductView{
			Name: "Mug", SelectionOK: true, Exact: true, Sellable: true,
			PriceCents: 43200, CompareCents: 48000, OnSale: true, Campaign: NewProductCampaign("autumn-picks", titleIn(tt.locale), tt.ends, now),
		}
		got := buyBox(t, tt.locale, &v)
		_, line, ok := strings.Cut(got, `<p class="goen-pdp__source">`)
		if !ok {
			t.Errorf("%s, %s: no source line in\n%s", tt.name, tt.locale, got)
			continue
		}
		line, _, _ = strings.Cut(line, "</p>")
		if !strings.Contains(line, `<a href="/s/autumn-picks">`+titleIn(tt.locale)+`</a>`) {
			t.Errorf("%s, %s: the source line does not link the campaign: %s", tt.name, tt.locale, line)
		}
		text := strings.NewReplacer("&nbsp;", "\u00a0").Replace(htmlTag.ReplaceAllString(line, ""))
		if strings.TrimSpace(text) != tt.want {
			t.Errorf("%s, %s: the source line reads %q, want %q", tt.name, tt.locale, text, tt.want)
		}
	}
}

var htmlTag = regexp.MustCompile(`<[^>]*>`)
