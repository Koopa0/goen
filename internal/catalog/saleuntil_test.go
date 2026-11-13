package catalog

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/tile"
)

// TestWithSaleUntil holds that a struck price on the shelf names the last day
// of its sale, and that a price with no end names none.
//
// The campaign's end is exclusive, so one that ends at midnight on Nov 23 ran
// through Nov 22.
func TestWithSaleUntil(t *testing.T) {
	t.Parallel()
	shop := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, shop)
	ends := time.Date(2026, 11, 23, 0, 0, 0, 0, shop)
	got := withSaleUntil([]tile.Tile{
		{Slug: "struck", SaleEndsAt: ends},
		{Slug: "plain"},
	}, now)
	want := []tile.Tile{
		{Slug: "struck", SaleEndsAt: ends, SaleUntil: shoptime.Date{Year: 2026, Month: time.November, Day: 22}},
		{Slug: "plain"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("withSaleUntil() mismatch (-want +got):\n%s", diff)
	}
}

// TestSearchEmptyNewestSaleUntil holds that the newest products the empty
// search offers say how long a struck price lasts, in Chinese and in English,
// under a product on sale and nowhere else.
func TestSearchEmptyNewestSaleUntil(t *testing.T) {
	t.Parallel()
	ends := time.Date(2026, 11, 23, 0, 0, 0, 0, time.FixedZone("CST", 8*3600))
	until := shoptime.LastDay(ends, ends)
	newest := []tile.Tile{
		{Slug: "tote", Name: "Tote", PriceCents: 80100, CompareCents: 89000, OnSale: true, InStock: true, SaleEndsAt: ends, SaleUntil: until, ImageURL: "/img/tote.webp", ImageAlt: "tote"},
		{Slug: "cream", Name: "Cream", PriceCents: 32000, InStock: true, ImageURL: "/img/cream.webp", ImageAlt: "cream"},
	}
	for _, tt := range []struct {
		locale i18n.Locale
		shown  string
	}{
		{i18n.ZhHant, "特價至"},
		{i18n.En, "On sale until"},
	} {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		page := renderComponent(t, ctx, Search(SearchMeta(ctx, "zzqxv"), SearchView{Query: "zzqxv", Newest: newest}))
		if got := strings.Count(page, `<span class="goen-tile__until">`); got != 1 {
			t.Errorf("%s: the empty search draws %d sale-end lines, want 1 (under the product on sale)", tt.locale, got)
			continue
		}
		start := strings.Index(page, `<span class="goen-tile__until">`)
		line := page[start : start+strings.Index(page[start:], "</span></span>")]
		if !strings.Contains(line, tt.shown) || !strings.Contains(line, `<time datetime="2026-11-22">`) {
			t.Errorf("%s: the sale-end line is %q, want %q and the day 2026-11-22", tt.locale, line, tt.shown)
		}
	}
}
