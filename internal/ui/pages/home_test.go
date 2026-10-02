package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// The shipping strip may name store pickup only where checkout offers it.
func TestTheShippingStripNamesPickupOnlyWhereItIsOffered(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		with := (&HomeView{PickupOffered: true}).ShippingBodyKey()
		without := (&HomeView{}).ShippingBodyKey()
		if with == without {
			t.Fatalf("%s: the strip says the same thing with and without pickup", locale)
		}
		pickup := map[i18n.Locale]string{i18n.ZhHant: "超商取貨", i18n.En: "pickup"}[locale]
		if !strings.Contains(i18n.T(ctx, with), pickup) {
			t.Errorf("%s: the pickup sentence does not name pickup", locale)
		}
		if strings.Contains(i18n.T(ctx, without), pickup) {
			t.Errorf("%s: the sentence for a shop without pickup names pickup", locale)
		}
	}
}

// A section heading's grey continuation drops to its own line on a phone and
// its separator is hidden there, but only from sight: a screen reader still
// hears the name, the separator and the rest as one sentence, in both
// languages.
func TestASectionHeadingKeepsItsSeparatorForAScreenReader(t *testing.T) {
	t.Parallel()
	for locale, sep := range map[i18n.Locale]string{i18n.ZhHant: "。", i18n.En: ". "} {
		page := renderIn(t, locale, Home(layouts.Page{}, HomeView{Row: ProductRow{
			Title: "秋季選物", Fact: "8 件商品", Href: "/s/autumn",
			Tiles: []ProductTile{{Slug: "a", Name: "A", PriceCents: 100, InStock: true}},
		}}))
		want := `<h2 id="row-heading" class="goen-home__heading">秋季選物<span class="goen-home__aside">` +
			`<span class="goen-home__sep">` + sep + `</span>8 件商品</span></h2>`
		if !strings.Contains(page, want) {
			t.Errorf("%s heading is not name, separator, rest:\n%s", locale, page[strings.Index(page, `id="row-heading"`)-4:][:200])
		}
	}
}
