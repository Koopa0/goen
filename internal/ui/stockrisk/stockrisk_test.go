package stockrisk

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/inventory"
)

const stockedAllWindow = 30 * 24 * time.Hour

// renderComponent renders c under ctx, which the caller owns so the locale stays
// explicit at the call site.
func renderComponent(t *testing.T, ctx context.Context, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(ctx, &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

func TestWarningFollowsTheEstimateAndItsWordsTheRange(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, tc := range []struct {
		name    string
		c       DaysCover
		urgent  bool
		warning string
	}{
		{"sold out", DaysCover{inventory.Cover{State: inventory.CoverSoldOut}}, true, ""},
		{"estimate at 29 with the range inside 30", DaysCover{inventory.Cover{State: inventory.CoverEstimated, Days: 29, Low: 20, High: 29}}, true, "Runs out within 30 days"},
		{"estimate at 29 with the range past 30", DaysCover{inventory.Cover{State: inventory.CoverEstimated, Days: 29, Low: 20, High: 50}}, true, "May run out within 30 days"},
		{"estimate at 30 is not marked", DaysCover{inventory.Cover{State: inventory.CoverEstimated, Days: 30, Low: 10, High: 40}}, false, ""},
		{"too few orders are not marked", DaysCover{inventory.Cover{State: inventory.CoverFewOrders}}, false, ""},
	} {
		if got := tc.c.Urgent(); got != tc.urgent {
			t.Errorf("%s: %+v.Urgent() = %v, want %v", tc.name, tc.c, got, tc.urgent)
		}
		if got := tc.c.Warning(ctx); got != tc.warning {
			t.Errorf("%s: %+v.Warning() = %q, want %q", tc.name, tc.c, got, tc.warning)
		}
	}
}

func TestAnEstimateUnderADayIsADay(t *testing.T) {
	t.Parallel()

	// 1 sellable at 150 units in 30 days is 0.2 days.
	c := Row{Risk: inventory.Risk{Sales: inventory.Sales{Sellable: 1, Sold: 150, Orders: 40, InStock: stockedAllWindow}}}.Estimate()
	if c.Days != 1 || c.Low != 1 || c.High != 1 {
		t.Errorf("0.2 days reads %+v, want 1, 1 and 1", c)
	}
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	if got := c.Figure(ctx); got != "About 1 day" {
		t.Errorf("Figure = %q, want About 1 day", got)
	}
	if got := c.Range(ctx); got != "" {
		t.Errorf("Range = %q, want none for a range of one day", got)
	}
}

func TestFigureRangeAndMarkAcrossTheScale(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, tc := range []struct {
		name   string
		c      DaysCover
		figure string
		rng    string
		urgent bool
	}{
		{"7 days", DaysCover{inventory.Cover{State: inventory.CoverEstimated, Days: 7, Low: 4, High: 12}}, "About 7 days", "90% range: 4–12 days", true},
		{"30 days is not marked", DaysCover{inventory.Cover{State: inventory.CoverEstimated, Days: 30, Low: 20, High: 45}}, "About 30 days", "90% range: 20–45 days", false},
		{"90 days is on the scale", DaysCover{inventory.Cover{State: inventory.CoverEstimated, Days: 90, Low: 60, High: 140}}, "About 90 days", "90% range: 60–90+ days", false},
		{"91 days is beyond it", DaysCover{inventory.Cover{State: inventory.CoverEstimated, Days: 91, Low: 60, High: 180}}, "More than 90 days", "90% range: 60–90+ days", false},
		{"beyond with a range off the scale", DaysCover{inventory.Cover{State: inventory.CoverEstimated, Days: 200, Low: 120, High: 400}}, "More than 90 days", "", false},
	} {
		if got := tc.c.Figure(ctx); got != tc.figure {
			t.Errorf("%s: Figure = %q, want %q", tc.name, got, tc.figure)
		}
		if got := tc.c.Range(ctx); got != tc.rng {
			t.Errorf("%s: Range = %q, want %q", tc.name, got, tc.rng)
		}
		if got := tc.c.Urgent(); got != tc.urgent {
			t.Errorf("%s: Urgent = %v, want %v", tc.name, got, tc.urgent)
		}
		if got := tc.c.Bar().Urgent; got != tc.urgent {
			t.Errorf("%s: Bar().Urgent = %v, want %v, the same as the ▲", tc.name, got, tc.urgent)
		}
	}
}

func TestStockRowIsNameAndFactsBesideOneFigure(t *testing.T) {
	t.Parallel()

	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		rows := []Row{{Risk: inventory.Risk{SKU: "OUT-1", Name: "Gone", Slug: "gone", SoldOutAt: time.Date(2026, time.October, 2, 4, 0, 0, 0, time.UTC)}, ReadAt: time.Date(2026, time.October, 7, 4, 0, 0, 0, time.UTC)},
			{Risk: inventory.Risk{SKU: "OUT-2", Name: "Long gone", Slug: "long-gone"}},
			{Risk: inventory.Risk{SKU: "EST-1", Name: "Going", Slug: "going", Sales: inventory.Sales{Sellable: 14, Sold: 43, Orders: 36, InStock: stockedAllWindow}}},
			{Risk: inventory.Risk{SKU: "EST-2", Name: "Later", Slug: "later", Sales: inventory.Sales{Sellable: 45, Sold: 30, Orders: 20, InStock: stockedAllWindow}}},
			{Risk: inventory.Risk{SKU: "FEW-1", Name: "Rare", Slug: "rare", Sales: inventory.Sales{Sellable: 9, Sold: 2, Orders: 2, InStock: stockedAllWindow}}},
		}
		html := renderComponent(t, ctx, Rows(rows, 0, 30))

		if got := strings.Count(html, `<li class="goen-report__row">`); got != 5 {
			t.Fatalf("%s: %d rows, want 5", loc, got)
		}
		for _, soldOut := range []string{"OUT-1", "OUT-2"} {
			if want := `<p class="goen-report__facts">` + soldOut + `</p>`; !strings.Contains(html, want) {
				t.Errorf("%s: a sold out row's fact line is not its SKU alone, want %s", loc, want)
			}
		}
		if strings.Contains(html, `class="goen-report__figure"`) {
			t.Errorf("%s: a row column wears the report headline tiles' class", loc)
		}
		if got := strings.Count(html, `<p class="goen-report__facts">`); got != 5 {
			t.Errorf("%s: %d fact lines, want one under each name", loc, got)
		}
		if got := strings.Count(html, "<svg class=\"goen-report__tri\""); got != 1 {
			t.Errorf("%s: %d warning triangles, want 1: only the estimate within the line", loc, got)
		}
		warning := DaysCover{inventory.Cover{State: inventory.CoverEstimated, Days: 10, Low: 7, High: 14}}.Warning(ctx)
		if want := fmt.Sprintf(`aria-label=%q`, warning); !strings.Contains(html, want) {
			t.Errorf("%s: the triangle lacks its text alternative %s in %s", loc, want, html)
		}
		if got := strings.Count(html, warning); got != 2 {
			t.Errorf("%s: the warning %q is written %d times, want twice, as the triangle's label and its tooltip", loc, warning, got)
		}
		if got := strings.Count(html, "goen-chartrangebar--urgent"); got != 1 {
			t.Errorf("%s: %d urgent bars, want 1", loc, got)
		}
		if got := strings.Count(html, `<div class="goen-chartrangebar`); got != 2 {
			t.Errorf("%s: %d bars, want the two estimated rows only", loc, got)
		}
		since := i18n.T(ctx, i18n.KeyAdminRepSoldOutSince)
		if want := strings.Replace(since, "%s", map[i18n.Locale]string{i18n.ZhHant: "10/2", i18n.En: "Oct\u00a02"}[loc], 1); strings.Count(html, want) != 1 {
			t.Errorf("%s: the sold-out row lacks %q, or the one already out before the window has it", loc, want)
		}
		if got := strings.Count(html, i18n.T(ctx, i18n.KeySoldOut)); got != 2 {
			t.Errorf("%s: sold out is said %d times, want once for each of two rows", loc, got)
		}
	}
}

func TestSoldOutSinceNamesTheYearOnlyWhenItIsNotThisOne(t *testing.T) {
	t.Parallel()

	readAt := time.Date(2026, time.January, 3, 4, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		soldOutAt time.Time
		want      map[i18n.Locale]string
	}{
		{"this year", time.Date(2026, time.January, 1, 4, 0, 0, 0, time.UTC), map[i18n.Locale]string{i18n.ZhHant: "1/1 起", i18n.En: "since Jan\u00a01"}},
		{"last year", time.Date(2025, time.December, 20, 4, 0, 0, 0, time.UTC), map[i18n.Locale]string{i18n.ZhHant: "2025\u00a0年 12\u00a0月 20\u00a0日 起", i18n.En: "since Dec\u00a020, 2025"}},
	} {
		for loc, want := range tc.want {
			ctx := i18n.WithLocale(t.Context(), loc)
			if got := (Row{Risk: inventory.Risk{SoldOutAt: tc.soldOutAt}, ReadAt: readAt}).SoldOutSince(ctx); got != want {
				t.Errorf("%s, %s: SoldOutSince = %q, want %q", tc.name, loc, got, want)
			}
		}
	}
}

func TestStockLeadDescribesTheDrawingAsDrawn(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		loc  i18n.Locale
		want []string
	}{
		{i18n.ZhHant, []string{"淡色是可能撐到的天數，短豎線是 30 天", "▲ 只標估計少於 30 天的"}},
		{i18n.En, []string{"The pale stretch is how long it may last and the short line marks 30 days", "▲ marks only an estimate under 30 days"}},
	} {
		ctx := i18n.WithLocale(t.Context(), tc.loc)
		html := renderComponent(t, ctx, Section(nil, 0, 30))
		for _, want := range tc.want {
			if !strings.Contains(html, want) {
				t.Errorf("%s: the stock lead lacks %q", tc.loc, want)
			}
		}
	}
}
