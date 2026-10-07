package admin

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

const stockedAllWindow = 30 * 24 * time.Hour

func TestEstimateStatesAndDays(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		row  StockRisk
		want CoverState
	}{
		{"sold out whatever it sold", StockRisk{Sellable: 0, Sold: 400, Orders: 90, InStock: stockedAllWindow}, CoverSoldOut},
		{"a negative sellable is sold out", StockRisk{Sellable: -2}, CoverSoldOut},
		{"nine orders are too few", StockRisk{Sellable: 5, Sold: 90, Orders: 9, InStock: stockedAllWindow}, CoverFewOrders},
		{"ten orders of one unit each are enough", StockRisk{Sellable: 5, Sold: 10, Orders: 10, InStock: stockedAllWindow}, CoverEstimated},
		{"a short time in stock is still estimated", StockRisk{Sellable: 5, Sold: 40, Orders: 20, InStock: 3 * 24 * time.Hour}, CoverEstimated},
	} {
		if got := tc.row.Estimate().State; got != tc.want {
			t.Errorf("%s: %+v.Estimate().State = %d, want %d", tc.name, tc.row, got, tc.want)
		}
	}
}

// Ten orders of ten units are 100 units: counted in units the SKU would
// clear the threshold with one order.
func TestEstimateCountsOrdersNotUnits(t *testing.T) {
	t.Parallel()

	r := StockRisk{Sellable: 50, Sold: 100, Orders: 1, InStock: stockedAllWindow}
	if got := r.Estimate().State; got != CoverFewOrders {
		t.Errorf("one order of 100 units: state %d, want CoverFewOrders %d", got, CoverFewOrders)
	}
}

func TestEstimateDividesByTheDaysInStock(t *testing.T) {
	t.Parallel()

	// 30 units over 15 days in stock is 2 a day: 40 sellable last 20 days. Over
	// 30 calendar days it would be 1 a day and read 40.
	r := StockRisk{Sellable: 40, Sold: 30, Orders: 15, InStock: 15 * 24 * time.Hour}
	if got := r.Estimate().Days; got != 20 {
		t.Errorf("%+v.Estimate().Days = %d, want 20", r, got)
	}
}

// The 90% interval of 10 orders is 0.54 to 1.70 times the observed rate
// (analytics.md, Poisson table), so 0.59 to 1.84 times the days.
func TestEstimateRangeMatchesThePoissonTable(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		orders    int64
		low, high float64
		tolerance float64
	}{
		{10, 0.59, 1.84, 0.02},
		{20, 0.69, 1.51, 0.02},
		{30, 0.74, 1.39, 0.02},
	} {
		r := StockRisk{Sellable: 1000, Sold: tc.orders * 2, Orders: tc.orders, InStock: stockedAllWindow}
		// 1000 sellable at 2 units per order over 30 days.
		c := r.Estimate()
		days := float64(c.Days)
		if got := float64(c.Low) / days; got < tc.low-tc.tolerance || got > tc.low+tc.tolerance {
			t.Errorf("%d orders: low is %.2f of the estimate, want %.2f", tc.orders, got, tc.low)
		}
		if got := float64(c.High) / days; got < tc.high-tc.tolerance || got > tc.high+tc.tolerance {
			t.Errorf("%d orders: high is %.2f of the estimate, want %.2f", tc.orders, got, tc.high)
		}
	}
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
		{"sold out", DaysCover{State: CoverSoldOut}, true, ""},
		{"estimate at 29 with the range inside 30", DaysCover{State: CoverEstimated, Days: 29, Low: 20, High: 29}, true, "Runs out within 30 days"},
		{"estimate at 29 with the range past 30", DaysCover{State: CoverEstimated, Days: 29, Low: 20, High: 50}, true, "May run out within 30 days"},
		{"estimate at 30 is not marked", DaysCover{State: CoverEstimated, Days: 30, Low: 10, High: 40}, false, ""},
		{"too few orders are not marked", DaysCover{State: CoverFewOrders}, false, ""},
	} {
		if got := tc.c.Urgent(); got != tc.urgent {
			t.Errorf("%s: %+v.Urgent() = %v, want %v", tc.name, tc.c, got, tc.urgent)
		}
		if got := tc.c.Warning(ctx); got != tc.warning {
			t.Errorf("%s: %+v.Warning() = %q, want %q", tc.name, tc.c, got, tc.warning)
		}
	}
}

func TestRankKeepsTwoGroupsEachWithItsOwnCap(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	rows := make([]StockRisk, 0, 2*coverMaxRows+4)
	for i := range coverMaxRows + 2 {
		rows = append(rows, StockRisk{SKU: fmt.Sprintf("OUT-%02d", i), SoldOutAt: at.Add(time.Duration(i) * time.Hour)})
	}
	for i := range coverMaxRows + 1 {
		rows = append(rows, StockRisk{
			SKU: fmt.Sprintf("EST-%02d", i), Sellable: int32(10 + i), Sold: 60, Orders: 30, InStock: stockedAllWindow,
		})
	}
	rows = append(rows, StockRisk{SKU: "FEW", Sellable: 1, Sold: 2, Orders: 2, InStock: stockedAllWindow})

	listed, more := RankStockRisk(rows)
	var soldOut, estimated []string
	for _, r := range listed {
		switch r.Estimate().State {
		case CoverSoldOut:
			soldOut = append(soldOut, r.SKU)
		case CoverEstimated:
			estimated = append(estimated, r.SKU)
		case CoverFewOrders:
		}
	}
	if len(soldOut) != coverMaxRows || more != 2 {
		t.Errorf("%d sold out rows listed and %d left off, want %d and 2", len(soldOut), more, coverMaxRows)
	}
	if soldOut[0] != "OUT-11" {
		t.Errorf("first sold out row is %s, want OUT-11, the one that ran out last", soldOut[0])
	}
	if len(estimated) != coverMaxRows || estimated[0] != "EST-00" {
		t.Errorf("estimated rows %v, want %d from EST-00: sold out rows must not take their places", estimated, coverMaxRows)
	}
	for i, r := range listed[:coverMaxRows] {
		if r.Estimate().State != CoverSoldOut {
			t.Errorf("row %d is %s, want the sold out group first", i, r.SKU)
		}
	}
}

func TestAnEstimateUnderADayIsADay(t *testing.T) {
	t.Parallel()

	// 1 sellable at 150 units in 30 days is 0.2 days.
	c := StockRisk{Sellable: 1, Sold: 150, Orders: 40, InStock: stockedAllWindow}.Estimate()
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
		{"7 days", DaysCover{State: CoverEstimated, Days: 7, Low: 4, High: 12}, "About 7 days", "90% range: 4–12 days", true},
		{"30 days is not marked", DaysCover{State: CoverEstimated, Days: 30, Low: 20, High: 45}, "About 30 days", "90% range: 20–45 days", false},
		{"90 days is on the scale", DaysCover{State: CoverEstimated, Days: 90, Low: 60, High: 140}, "About 90 days", "90% range: 60–90+ days", false},
		{"91 days is beyond it", DaysCover{State: CoverEstimated, Days: 91, Low: 60, High: 180}, "More than 90 days", "90% range: 60–90+ days", false},
		{"beyond with a range off the scale", DaysCover{State: CoverEstimated, Days: 200, Low: 120, High: 400}, "More than 90 days", "", false},
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

func TestStockRowsCarryTheirRangeAndWindowInText(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	html := renderComponent(t, ctx, Report(layouts.Page{Title: "Reports"}, &ReportView{
		Days: 7, StockDays: 30, Windows: []int32{7, 30, 90},
		AtRisk: []StockRisk{
			{SKU: "OUT-1", Name: "Gone", Slug: "gone"},
			{SKU: "EST-1", Name: "Going", Slug: "going", Sellable: 20, Sold: 30, Orders: 15, InStock: stockedAllWindow},
			{SKU: "FEW-1", Name: "Rare", Slug: "rare", Sellable: 9, Sold: 2, Orders: 2, InStock: stockedAllWindow},
		},
	}))

	for _, want := range []string{
		i18n.T(ctx, i18n.KeySoldOut),
		"About 20 days",
		"90% range: ",
		"May run out within 30 days",
		i18n.T(ctx, i18n.KeyAdminRepFewSold),
		"9 sellable",
		"2 units",
		"2 orders",
		"sold in the last 30 days",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the stock rows lack %q", want)
		}
	}
	if got := strings.Count(html, `<div class="goen-chartrangebar`); got != 1 {
		t.Errorf("%d range bars drawn, want 1: only the estimated row has one", got)
	}
	if strings.Contains(html, "style=") {
		t.Error("the stock rows carry a style attribute")
	}
}

func TestMoreSoldOutLinksToTheStockDeskFilter(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := &ReportView{
		Days: 30, StockDays: 30, Windows: []int32{7, 30, 90}, MoreSoldOut: 2,
		AtRisk: []StockRisk{{SKU: "OUT-1", Name: "Gone", Slug: "gone"}},
	}
	html := renderComponent(t, ctx, Report(layouts.Page{Title: "Reports"}, view))
	if want := `<a href="/admin/stock?soldout=1">2 more items sold out</a>`; !strings.Contains(html, want) {
		t.Errorf("the stock section lacks %s", want)
	}
	view.MoreSoldOut = 0
	if html := renderComponent(t, ctx, Report(layouts.Page{Title: "Reports"}, view)); strings.Contains(html, "/admin/stock?soldout=1") {
		t.Error("the stock section links to the sold out filter with none left off")
	}
}

func TestStockDeskListsDaysCoverAboveTheSearch(t *testing.T) {
	t.Parallel()

	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		rows := []StockRisk{
			{SKU: "OUT-1", Name: "Gone", Slug: "gone"},
			{SKU: "EST-1", Name: "Going", Slug: "going", Sellable: 20, Sold: 60, Orders: 30, InStock: stockedAllWindow},
		}
		html := renderComponent(t, ctx, Variants(layouts.Page{}, VariantsView{ShowCover: true, AtRisk: rows, MoreSoldOut: 3}))

		heading := strings.Index(html, i18n.T(ctx, i18n.KeyAdminRepStock))
		search := strings.Index(html, `id="stock-search"`)
		out, est := strings.Index(html, "OUT-1"), strings.Index(html, "EST-1")
		if heading < 0 || search < 0 || heading > search {
			t.Errorf("%s: days cover heading at %d, search at %d, want the heading first", loc, heading, search)
		}
		if out < 0 || est < 0 || out > est {
			t.Errorf("%s: sold out row at %d, estimated row at %d, want sold out first", loc, out, est)
		}
		for _, want := range []string{
			i18n.Count(ctx, i18n.KeyAdminRepMoreSoldOut, 3, 3), "goen-chartrangebar",
		} {
			if !strings.Contains(html, want) {
				t.Errorf("%s: stock desk lacks %q", loc, want)
			}
		}
	}
}

func TestStockDeskSaysSoWhenNothingNeedsRestocking(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	html := renderComponent(t, ctx, Variants(layouts.Page{}, VariantsView{ShowCover: true}))
	if want := i18n.Count(ctx, i18n.KeyAdminRepStockEmpty, CoverWindowDays, CoverWindowDays); !strings.Contains(html, want) {
		t.Errorf("an empty days cover list does not say %q", want)
	}
}

func TestStockDeskLaterPagesLeaveDaysCoverOut(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	html := renderComponent(t, ctx, Variants(layouts.Page{}, VariantsView{Variants: []Variant{{SKU: "A-1", ProductName: "x"}}}))
	if strings.Contains(html, i18n.T(ctx, i18n.KeyAdminRepStock)) {
		t.Error("a page without ShowCover still lists the days cover section")
	}
}

// The report and the stock desk draw the section from one component.
func TestReportAndStockDeskShareTheDaysCoverSection(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	rows := []StockRisk{{SKU: "OUT-1", Name: "Gone", Slug: "gone"}}
	section := renderComponent(t, ctx, StockRiskSection(rows, 0, CoverWindowDays))
	for name, page := range map[string]string{
		"stock desk": renderComponent(t, ctx, Variants(layouts.Page{}, VariantsView{ShowCover: true, AtRisk: rows})),
		"report":     renderComponent(t, ctx, Report(layouts.Page{}, &ReportView{Days: 30, Windows: []int32{7, 30, 90}, AtRisk: rows, StockDays: CoverWindowDays})),
	} {
		if !strings.Contains(page, section) {
			t.Errorf("%s does not contain the shared days cover section", name)
		}
	}
}

func TestStockRowIsNameAndFactsBesideOneFigure(t *testing.T) {
	t.Parallel()

	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		rows := []StockRisk{
			{SKU: "OUT-1", Name: "Gone", Slug: "gone", SoldOutAt: time.Date(2026, time.October, 2, 4, 0, 0, 0, time.UTC), ReadAt: time.Date(2026, time.October, 7, 4, 0, 0, 0, time.UTC)},
			{SKU: "OUT-2", Name: "Long gone", Slug: "long-gone"},
			{SKU: "EST-1", Name: "Going", Slug: "going", Sellable: 14, Sold: 43, Orders: 36, InStock: stockedAllWindow},
			{SKU: "EST-2", Name: "Later", Slug: "later", Sellable: 45, Sold: 30, Orders: 20, InStock: stockedAllWindow},
			{SKU: "FEW-1", Name: "Rare", Slug: "rare", Sellable: 9, Sold: 2, Orders: 2, InStock: stockedAllWindow},
		}
		html := renderComponent(t, ctx, StockRiskRows(rows, 0, 30))

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
		warning := DaysCover{State: CoverEstimated, Days: 10, Low: 7, High: 14}.Warning(ctx)
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
			if got := (StockRisk{SoldOutAt: tc.soldOutAt, ReadAt: readAt}).SoldOutSince(ctx); got != want {
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
		html := renderComponent(t, ctx, StockRiskSection(nil, 0, 30))
		for _, want := range tc.want {
			if !strings.Contains(html, want) {
				t.Errorf("%s: the stock lead lacks %q", tc.loc, want)
			}
		}
	}
}

func TestListsShareOneRowSkeleton(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	stock := renderComponent(t, ctx, StockRiskRows([]StockRisk{{SKU: "EST-1", Name: "Going", Slug: "going", Sellable: 14, Sold: 43, Orders: 36, InStock: stockedAllWindow}}, 0, 30))
	report := renderComponent(t, ctx, Report(layouts.Page{Title: "Reports"}, &ReportView{
		Days: 30, Windows: []int32{7, 30, 90}, Placed: 3, Committed: 3, Orders: 3,
		Sellers: []Seller{{Slug: "a", Name: "Alpha", Brand: "Aurora", Units: 40}, {Slug: "b", Name: "Beta", Units: 10}},
	}))
	for name, html := range map[string]string{"stock": stock, "best sellers": report} {
		for _, want := range []string{
			`<li class="goen-report__row"><div class="goen-report__what"><a class="goen-report__name" href="/admin/products/`,
			`<p class="goen-report__facts">`,
			`<div class="goen-report__plot">`,
		} {
			if !strings.Contains(html, want) {
				t.Errorf("%s list lacks the shared row markup %s", name, want)
			}
		}
	}
}

func TestSellerFactsNameTheBrandThenWhatSold(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	got := Seller{Brand: "Aurora", Units: 4, RevenueCents: 120000}.Facts(ctx)
	if want := "Aurora · 4 units sold · Product gross NT$1,200"; got != want {
		t.Errorf("Seller.Facts = %q, want %q", got, want)
	}
	if got := (Seller{Units: 4, RevenueCents: 120000}).Facts(ctx); strings.HasPrefix(got, " ·") || strings.HasPrefix(got, "·") {
		t.Errorf("Seller.Facts without a brand = %q, want no leading separator", got)
	}
}
