package admin

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/chart"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestAnEmptySalesWindowStillListsStockAtRisk(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Report(layouts.Page{Title: "報表"}, &ReportView{
		Days:    30,
		Windows: []int32{7, 30, 90},
		AtRisk: []StockRisk{{
			SKU: "RISK-SKU-1", Name: "Shrinking SKU", Slug: "shrinking-sku",
			Sellable: 2, Sold: 8, Orders: 3,
		}},
	}))

	empty := i18n.T(ctx, i18n.KeyAdminRepEmpty)
	if !strings.Contains(html, empty) {
		t.Errorf("a zero-order window does not keep the no-orders sales state %q", empty)
	}
	if !strings.Contains(html, "RISK-SKU-1") {
		t.Error("a zero-order window hides an at-risk SKU that was already queried")
	}
	if !strings.Contains(html, "Shrinking SKU") {
		t.Error("a zero-order window hides the at-risk product name")
	}
	if !strings.Contains(html, i18n.T(ctx, i18n.KeyAdminRepStock)) {
		t.Error("a zero-order window drops the stock-at-risk heading")
	}
	if strings.Contains(html, `class="goen-report__figures"`) {
		t.Error("a zero-order window still paints the revenue strip")
	}
}

// A window is empty only when it has neither new orders nor refunds: a refund
// for an older order is the owner's return figure and must not be hidden.
func TestAReportWindowIsEmptyOnlyWithoutOrdersAndRefunds(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	empty := i18n.T(ctx, i18n.KeyAdminRepEmpty)
	windows := []int32{7, 30, 90}

	for _, tc := range []struct {
		name      string
		view      ReportView
		wantEmpty bool
	}{
		{"orders and revenue without refunds", ReportView{
			Days: 7, Windows: windows, Placed: 1, Committed: 1, Orders: 1, RevenueCents: 1000,
		}, false},
		{"refunds without orders", ReportView{
			Days: 7, Windows: windows, RefundedCents: 12500,
		}, false},
		{"neither orders nor refunds", ReportView{
			Days: 7, Windows: windows,
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.view.Empty(); got != tc.wantEmpty {
				t.Errorf("Empty() = %v, want %v", got, tc.wantEmpty)
			}
			html := renderToString(t, Report(layouts.Page{Title: "報表"}, &tc.view))
			if got := strings.Contains(html, empty); got != tc.wantEmpty {
				t.Errorf("no-orders state rendered = %v, want %v", got, tc.wantEmpty)
			}
			if got := strings.Contains(html, `class="goen-report__figures"`); got == tc.wantEmpty {
				t.Errorf("revenue strip rendered = %v, want %v", got, !tc.wantEmpty)
			}
		})
	}
}

func TestBestSellerGrossCannotBeMistakenForOrderRevenue(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale              i18n.Locale
		gross, units, scope string
	}{
		{i18n.ZhHant, "商品毛額 NT$1,000", "售出 2 件", "含稅成交單價乘售出數量計算，未扣訂單折扣或退款，不含運費"},
		{i18n.En, "Product gross NT$1,000", "2 units sold", "tax-inclusive sale unit price multiplied by units sold, before order discounts or refunds and excluding shipping"},
	} {
		t.Run(string(tt.locale), func(t *testing.T) {
			t.Parallel()
			var b bytes.Buffer
			view := ReportView{Placed: 1, Orders: 1, Committed: 1, RevenueCents: 90000, Sellers: []Seller{{Name: "Discounted item", Slug: "discounted", Units: 2, RevenueCents: 100000}}}
			if err := Report(layouts.Page{Title: "Reports"}, &view).Render(i18n.WithLocale(t.Context(), tt.locale), &b); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{tt.gross, tt.units, tt.scope, view.Revenue()} {
				if !strings.Contains(b.String(), want) {
					t.Errorf("report lacks %q", want)
				}
			}
		})
	}
}

func TestReportViewCompletion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		placed, committed int64
		want              string
	}{
		{placed: 0, committed: 0, want: "—"},
		{placed: 3, committed: 2, want: "2 of 3 orders"},
		{placed: 3, committed: 1, want: "1 of 3 orders"},
		{placed: 19, committed: 19, want: "19 of 19 orders"},
		{placed: 20, committed: 1, want: "5%"},
		{placed: 30, committed: 20, want: "67%"},
		{placed: 40, committed: 1, want: "3%"},
		{placed: 300, committed: 199, want: "66%"},
		{placed: 200, committed: 199, want: "99%"},
		{placed: 201, committed: 1, want: "1%"},
		{placed: 200, committed: 200, want: "100%"},
		{placed: 25, committed: 0, want: "0%"},
	}
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, tt := range tests {
		view := ReportView{Placed: tt.placed, Committed: tt.committed}
		got := view.Completion(ctx)
		if got != tt.want {
			t.Errorf("Completion() with %d of %d placed = %q, want %q", tt.committed, tt.placed, got, tt.want)
		}
	}
}

func TestReportSaysTheCountOnceForFewOrders(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	html := renderComponent(t, ctx, Report(layouts.Page{Title: "Reports"}, &ReportView{
		Days: 7, Windows: []int32{7, 30, 90}, Placed: 3, Committed: 2, Orders: 2, RevenueCents: 1000,
	}))

	counts := fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepCounts), "2", "3")
	if n := strings.Count(html, counts); n != 1 {
		t.Errorf("the report shows %q %d times, want once", counts, n)
	}
}

func TestBestSellersAreDrawnAsBarsOnOneScale(t *testing.T) {
	t.Parallel()

	html := renderToString(t, Report(layouts.Page{Title: "Reports"}, &ReportView{
		Days: 30, Windows: []int32{7, 30, 90}, Placed: 3, Committed: 3, Orders: 3,
		Sellers: []Seller{
			{Slug: "a", Name: "Alpha", Units: 40},
			{Slug: "b", Name: "Beta", Units: 40},
			{Slug: "c", Name: "Gamma", Units: 10},
		},
	}))

	if got := strings.Count(html, `class="goen-chartbar__fill"`); got != 3 {
		t.Fatalf("report shows %d bars, want 3", got)
	}
	if got := strings.Count(html, `width="100.00%"`); got != 2 {
		t.Errorf("report shows %d bars across the full track, want the 2 tied sellers", got)
	}
	if !strings.Contains(html, `width="25.00%"`) {
		t.Error("report does not draw 10 units at a quarter of 40")
	}
	for _, count := range []string{"40", "10"} {
		if !strings.Contains(html, `<span class="goen-chartbar__label">`+count+`</span>`) {
			t.Errorf("report omits the count %s beside its bar", count)
		}
	}
	if !strings.Contains(html, "Alpha") || !strings.Contains(html, "Gamma") {
		t.Error("report lost the product names the bars sit beside")
	}
}

func TestOneSellerDrawsNoBar(t *testing.T) {
	t.Parallel()

	html := renderToString(t, Report(layouts.Page{Title: "Reports"}, &ReportView{
		Days: 30, Windows: []int32{7, 30, 90}, Placed: 1, Committed: 1, Orders: 1,
		Sellers: []Seller{{Slug: "a", Name: "Alpha", Units: 40}},
	}))
	if strings.Contains(html, "goen-chartbar") {
		t.Error("report draws a bar for a single best seller, which compares it with nothing")
	}
	if !strings.Contains(html, "Alpha") {
		t.Error("report lost the single best seller's row")
	}
}

func TestNoSellersDrawNoBars(t *testing.T) {
	t.Parallel()

	html := renderToString(t, Report(layouts.Page{Title: "Reports"}, &ReportView{
		Days: 30, Windows: []int32{7, 30, 90}, Placed: 1, Committed: 1, Orders: 1,
	}))
	if strings.Contains(html, "goen-chartbar") {
		t.Error("report draws a bar with no best sellers")
	}
}

func TestReportSetsEachFigureAgainstThePreviousPeriod(t *testing.T) {
	t.Parallel()

	// Orders of 1,000 each, so the sum of squares follows from the count.
	period := func(orders int64) (revenue int64, squares float64) {
		return orders * 100000, float64(orders) * 100000 * 100000
	}
	view := func(orders, prevOrders int64) ReportView {
		revenue, squares := period(orders)
		prevRevenue, prevSquares := period(prevOrders)
		return ReportView{
			Days: 30, Orders: orders, RevenueCents: revenue, RevenueSquares: squares,
			Previous: PreviousFigures{Orders: prevOrders, RevenueCents: prevRevenue, RevenueSquares: prevSquares},
		}
	}
	tests := []struct {
		name                string
		v                   ReportView
		wantOrders, wantRev string
	}{
		{"no orders before", view(40, 0),
			"No paid orders in the previous 30 days", "No paid orders in the previous 30 days"},
		{"too few orders now", view(19, 40),
			"Previous 30 days: 40", "Previous 30 days: NT$40,000"},
		{"too few orders before", view(40, 19),
			"Previous 30 days: 19", "Previous 30 days: NT$19,000"},
		{"a change within the noise", view(60, 50),
			"Previous 30 days: 50", "Previous 30 days: NT$50,000"},
		{"ten percent more, within the noise", view(110, 100),
			"Previous 30 days: 100", "Previous 30 days: NT$100,000"},
		{"clearly more", view(150, 100),
			"50% more than the previous 30 days", "50% more than the previous 30 days"},
		{"clearly less", view(100, 150),
			"33% less than the previous 30 days", "33% less than the previous 30 days"},
		{"too small to round to a percent", view(1_000_000, 1_002_900),
			"About the same as the previous 30 days", "About the same as the previous 30 days"},
	}
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, tt := range tests {
		if got := tt.v.OrdersAgainst(ctx); got != tt.wantOrders {
			t.Errorf("%s: OrdersAgainst() = %q, want %q", tt.name, got, tt.wantOrders)
		}
		if got := tt.v.RevenueAgainst(ctx); got != tt.wantRev {
			t.Errorf("%s: RevenueAgainst() = %q, want %q", tt.name, got, tt.wantRev)
		}
	}
}

func TestReportCompletionAgainstThePreviousPeriod(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		previous PreviousFigures
		want     string
	}{
		{"no orders placed before", PreviousFigures{}, "No orders in the previous 30 days"},
		{"fewer than twenty placed before", PreviousFigures{Placed: 6, Committed: 0}, "Previous 30 days: 0 of 6 orders"},
		{"twenty placed before", PreviousFigures{Placed: 20, Committed: 10}, "Previous 30 days: 50%"},
	}
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, tt := range tests {
		v := ReportView{Days: 30, Previous: tt.previous}
		if got := v.CompletionAgainst(ctx); got != tt.want {
			t.Errorf("%s: CompletionAgainst() = %q, want %q", tt.name, got, tt.want)
		}
	}

	zh := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, tt := range []struct {
		previous PreviousFigures
		want     string
	}{
		{PreviousFigures{}, "前 30 天沒有訂單"},
		{PreviousFigures{Placed: 6}, "前 30 天：0 / 6 筆"},
	} {
		v := ReportView{Days: 30, Previous: tt.previous}
		if got := v.CompletionAgainst(zh); got != tt.want {
			t.Errorf("CompletionAgainst(%+v) = %q, want %q", tt.previous, got, tt.want)
		}
	}
}

func TestReportRevenueNeedsMoreThanOrderCountToCallAChange(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	// Thirty orders each side, 20% apart in revenue, but one huge order now
	// makes the sum of squares large enough that the gap is noise.
	v := ReportView{
		Days: 30, Orders: 30, RevenueCents: 1_200_000, RevenueSquares: 1e12,
		Previous: PreviousFigures{Orders: 30, RevenueCents: 1_000_000, RevenueSquares: 1e11},
	}
	if got, want := v.RevenueAgainst(ctx), "Previous 30 days: NT$10,000"; got != want {
		t.Errorf("RevenueAgainst() = %q, want %q", got, want)
	}
	v.RevenueSquares = 1e9
	v.Previous.RevenueSquares = 1e9
	if got, want := v.RevenueAgainst(ctx), "20% more than the previous 30 days"; got != want {
		t.Errorf("RevenueAgainst() with small orders = %q, want %q", got, want)
	}
}

func TestReportHeadingNamesBothPeriodsByShopDay(t *testing.T) {
	t.Parallel()

	at := func(m time.Month, d int) shoptime.Date {
		return shoptime.DateOf(time.Date(2026, m, d, 4, 0, 0, 0, time.UTC), time.Date(2026, time.October, 5, 4, 0, 0, 0, time.UTC))
	}
	v := ReportView{
		From: at(time.September, 6), To: at(time.October, 5),
		Previous: PreviousFigures{From: at(time.August, 7), To: at(time.September, 5)},
	}
	for locale, want := range map[i18n.Locale]string{
		i18n.ZhHant: "9/6–10/5 · 對照 8/7–9/5",
		i18n.En:     "Sep 6–Oct 5 · against Aug 7–Sep 5",
	} {
		if got := v.Heading(i18n.WithLocale(t.Context(), locale)); got != want {
			t.Errorf("Heading() in %s = %q, want %q", locale, got, want)
		}
	}
}

// dailyView is a period of thirty days, with orders on the first orderDays of
// them, each of NT$1,000, and a previous period of the same shape.
func dailyView(orderDays int) ReportView {
	series := func(each int64) chart.Series {
		s := chart.Series{Partial: true}
		for i := range 30 {
			b := chart.Bucket{Day: time.Date(2026, 9, 6+i, 0, 0, 0, 0, time.UTC)}
			if i < orderDays {
				b.Value = each
			}
			s.Buckets = append(s.Buckets, b)
		}
		return s
	}
	orders := int64(orderDays) * 30
	return ReportView{
		Days: 30, Windows: []int32{7, 30, 90}, Placed: orders, Committed: orders,
		Orders: orders, RevenueCents: orders * 100000, RevenueSquares: float64(orders) * 1e10,
		Previous: PreviousFigures{Orders: orders, RevenueCents: orders * 100000, RevenueSquares: float64(orders) * 1e10},
		Daily:    DailyRevenue{Current: series(3_000_000), Previous: series(3_000_000), Cut: "15:20"},
	}
}

func TestRunningTotalsAreDrawnFromSevenDaysWithOrders(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		orderDays int
		want      bool
	}{
		{6, false},
		{7, true},
	} {
		v := dailyView(tc.orderDays)
		html := renderToString(t, Report(layouts.Page{Title: "報表"}, &v))
		if got := strings.Contains(html, `class="goen-chart"`); got != tc.want {
			t.Errorf("a period with orders on %d days draws the running totals = %v, want %v", tc.orderDays, got, tc.want)
		}
		if got := strings.Contains(html, i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyAdminRepRunning)); got != tc.want {
			t.Errorf("a period with orders on %d days shows the running totals heading = %v, want %v", tc.orderDays, got, tc.want)
		}
		if !strings.Contains(html, `class="goen-report__figures"`) {
			t.Errorf("a period with orders on %d days loses its tiles", tc.orderDays)
		}
	}
}

func TestRunningTotalsSaySoWhenTheDaysCouldNotBeRead(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	v := dailyView(7)
	v.Daily = DailyRevenue{}
	v.DailyUnavailable = true
	html := renderComponent(t, ctx, Report(layouts.Page{Title: "Reports"}, &v))

	if want := `<p class="goen-admin__hint" role="status">` + strings.ReplaceAll(i18n.T(ctx, i18n.KeyAdminRepRunningUnavailable), "'", "&#39;") + `</p>`; !strings.Contains(html, want) {
		t.Errorf("the page does not say the chart is unavailable: want %s", want)
	}
	if strings.Contains(html, `class="goen-chart"`) {
		t.Error("an unreadable chart is drawn")
	}
	if !strings.Contains(html, `class="goen-report__figures"`) {
		t.Error("an unreadable chart takes the tiles with it")
	}
}

func TestRunningTotalCaptionAndSourceLineUseTheTilesWordingAndTheCutTime(t *testing.T) {
	t.Parallel()

	en := i18n.WithLocale(t.Context(), i18n.En)
	zh := i18n.WithLocale(t.Context(), i18n.ZhHant)

	// 210 orders of NT$1,000 on each side, so the revenue gap is noise.
	same := dailyView(7)
	if got, want := same.RunningTotal(en).Caption, "Revenue over 30 days: NT$210,000. Previous 30 days: NT$210,000."; got != want {
		t.Errorf("caption without a difference = %q, want %q", got, want)
	}
	// Far more orders now: the sentence is the tile's, a percentage.
	more := dailyView(7)
	more.Previous.Orders, more.Previous.RevenueCents, more.Previous.RevenueSquares = 100, 10_000_000, 1e12
	more.Orders, more.RevenueCents, more.RevenueSquares = 400, 40_000_000, 4e12
	if got, want := more.RunningTotal(zh).Caption, "30 天營收 NT$400,000，比前 30 天多 300%。"; got != want {
		t.Errorf("caption with a difference = %q, want %q", got, want)
	}
	if got, want := more.RunningTotal(zh).Note, "只計入已付款的訂單，依下單時間。今天到 15:20 為止，前 30 天同樣算到 15:20。"; got != want {
		t.Errorf("source line = %q, want %q", got, want)
	}
	if got, want := more.RunningTotal(zh).Previous.Label, "前 30 天"; got != want {
		t.Errorf("previous legend label = %q, want %q: the page's one name for that period", got, want)
	}
	if got, want := more.RunningTotal(en).Previous.Label, "Previous 30 days"; got != want {
		t.Errorf("previous legend label = %q, want %q", got, want)
	}
	if got, want := more.RunningTotal(en).PartialLabel, "up to 15:20"; got != want {
		t.Errorf("last row's label = %q, want %q", got, want)
	}
}

// paidView is a period of n shop days up to Monday 2026-10-05, with the paid
// orders of the days at the given indexes, and the clock it is counted up to.
func paidView(n int, at map[int]int64) ReportView {
	days := make([]chart.Bucket, n)
	for i := range days {
		days[i].Day = time.Date(2026, 10, 5-(n-1)+i, 0, 0, 0, 0, time.UTC)
		days[i].Value = at[i]
	}
	return ReportView{
		Days: n, Windows: []int32{7, 30, 90}, Placed: 10, Committed: 10, Orders: 10,
		From: shoptime.Date{Year: 2026, Month: time.September, Day: 29}, To: shoptime.Date{Year: 2026, Month: time.October, Day: 5},
		Daily: DailyRevenue{Cut: "15:20"},
		Paid:  PaidDays{Days: chart.Series{Buckets: days, Partial: true}},
	}
}

func TestPaidOrdersAreToldInASentenceBelowThreeDays(t *testing.T) {
	t.Parallel()

	latest := shoptime.Date{Year: 2026, Month: time.September, Day: 21}
	for _, tc := range []struct {
		name         string
		view         ReportView
		locale       i18n.Locale
		wantSentence string
	}{
		{"none, with the latest, zh", paidViewWithLatest(7, nil, &latest), i18n.ZhHant, "9/29–10/5 沒有已付款的訂單。最近一筆在 9 月 21 日。"},
		{"none, with the latest, en", paidViewWithLatest(7, nil, &latest), i18n.En, "No paid orders Sep 29–Oct 5. The latest was on Sep 21."},
		{"none, never any, en", paidView(7, nil), i18n.En, "No paid orders Sep 29–Oct 5."},
		{"one day, zh", paidView(7, map[int]int64{6: 2}), i18n.ZhHant, "9/29–10/5 只有 1 天有已付款訂單：10/5 有 2 筆。"},
		{"one day, en", paidView(7, map[int]int64{6: 2}), i18n.En, "Sep 29–Oct 5 had paid orders on 1 day only: 2 orders on Oct 5."},
		{"two days, zh", paidView(7, map[int]int64{4: 1, 6: 2}), i18n.ZhHant, "9/29–10/5 只有 2 天有已付款訂單：10/3 有 1 筆、10/5 有 2 筆。"},
		{"two days, en", paidView(7, map[int]int64{4: 1, 6: 2}), i18n.En, "Sep 29–Oct 5 had paid orders on 2 days only: 1 order on Oct 3, 2 orders on Oct 5."},
	} {
		v := tc.view
		got := spaced(v.PaidSentence(i18n.WithLocale(t.Context(), tc.locale)))
		if got != tc.wantSentence {
			t.Errorf("%s: PaidSentence = %q, want %q", tc.name, got, tc.wantSentence)
		}
		if v.ShowsPaidColumns() {
			t.Errorf("%s: columns are drawn for %d days with orders, want a sentence", tc.name, len(tc.view.Paid.Days.Buckets))
		}
		html := spaced(renderComponent(t, i18n.WithLocale(t.Context(), tc.locale), Report(layouts.Page{Title: "x"}, &v)))
		if !strings.Contains(html, `<p class="goen-admin__hint">`+tc.wantSentence+`</p>`) {
			t.Errorf("%s: the page does not hold the sentence %q in place of the chart", tc.name, tc.wantSentence)
		}
		if strings.Contains(html, "goen-chart__hue") {
			t.Errorf("%s: a chart is drawn", tc.name)
		}
	}
}

// spaced reads the no-break spaces of a short date as the plain spaces they look like.
func spaced(s string) string { return strings.ReplaceAll(s, "\u00a0", " ") }

func paidViewWithLatest(n int, at map[int]int64, latest *shoptime.Date) ReportView {
	v := paidView(n, at)
	v.Paid.Latest = latest
	return v
}

func TestPaidOrdersFromThreeDaysAreColumnsCaptionedByTheirCount(t *testing.T) {
	t.Parallel()

	v := paidView(30, map[int]int64{3: 2, 10: 1, 29: 4})
	en := i18n.WithLocale(t.Context(), i18n.En)
	if got, want := v.PaidSentence(en), "Paid orders came in on 3 days of this period."; got != want {
		t.Errorf("PaidSentence = %q, want %q", got, want)
	}
	html := renderComponent(t, en, Report(layouts.Page{Title: "Reports"}, &v))
	for _, want := range []string{
		"Paid orders per day",
		`<figcaption class="goen-chart__caption">Paid orders came in on 3 days of this period.</figcaption>`,
		`<p class="goen-chart__note">Paid orders only, by the time placed. Today is counted up to 15:20.</p>`,
		`<th scope="col">Paid orders</th>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the page does not contain %s", want)
		}
	}
	if strings.Contains(html, "goen-chart__yaxis") {
		t.Error("three days with orders draw an axis")
	}
}

func TestBusiestDayAndItsTies(t *testing.T) {
	t.Parallel()

	zh, en := i18n.ZhHant, i18n.En
	// 30 days up to 10/5: index 22 is 9/28. Seven of them have orders.
	base := map[int]int64{20: 3, 21: 4, 22: 2, 23: 1, 24: 3, 25: 2, 29: 4}
	with := func(extra map[int]int64) ReportView {
		at := map[int]int64{}
		for k, val := range base {
			at[k] = val
		}
		for k, val := range extra {
			at[k] = val
		}
		return paidView(30, at)
	}
	one := with(map[int]int64{22: 9})
	two := with(map[int]int64{22: 9, 24: 9})
	three := with(map[int]int64{22: 9, 24: 9, 25: 9})
	many := with(map[int]int64{22: 9, 24: 9, 25: 9, 20: 9})
	for _, tc := range []struct {
		name         string
		v            ReportView
		locale       i18n.Locale
		wantSentence string
	}{
		{"one, zh", one, zh, "最多的一天是 9 月 28 日，9 筆；今天到 15:20 為止 4 筆。"},
		{"one, en", one, en, "The busiest day was Sep 28, with 9 orders; today up to 15:20, 4 orders."},
		{"two, en", two, en, "The busiest days were Sep 28, Sep 30, with 9 orders each; today up to 15:20, 4 orders."},
		{"three, zh", three, zh, "最多的是 9 月 28 日、9 月 30 日、10 月 1 日，各 9 筆；今天到 15:20 為止 4 筆。"},
		{"four, en", many, en, "4 days tied for the most, 9 orders each; today up to 15:20, 4 orders."},
		{"today one order, en", with(map[int]int64{22: 9, 29: 1}), en, "The busiest day was Sep 28, with 9 orders; today up to 15:20, 1 order."},
	} {
		if got := spaced(tc.v.PaidSentence(i18n.WithLocale(t.Context(), tc.locale))); got != tc.wantSentence {
			t.Errorf("%s: PaidSentence = %q, want %q", tc.name, got, tc.wantSentence)
		}
	}
}

func TestPaidOrdersOfNinetyDaysAreToldByTheSevenDaysTheyAreDrawnIn(t *testing.T) {
	t.Parallel()

	// 90 days up to 10/5 start on 7/8. The busiest 7 days start on 9/29.
	v := paidView(90, map[int]int64{0: 1, 40: 2, 60: 3, 83: 5, 85: 4, 88: 2, 89: 1})
	en := i18n.WithLocale(t.Context(), i18n.En)
	if got, want := v.PaidHeading(en), "Paid orders per 7 days"; got != want {
		t.Errorf("PaidHeading = %q, want %q", got, want)
	}
	if got, want := spaced(v.PaidSentence(en)), "The busiest stretch was the 7 days from Sep 29, with 12 orders."; got != want {
		t.Errorf("PaidSentence = %q, want %q", got, want)
	}
	if got, want := v.PaidColumns(en).DayHeading, "7 days from"; got != want {
		t.Errorf("the table's day heading = %q, want %q", got, want)
	}
	thirty := paidView(30, nil)
	if got, want := thirty.PaidHeading(en), "Paid orders per day"; got != want {
		t.Errorf("PaidHeading of 30 days = %q, want %q", got, want)
	}
}

func TestPaidOrdersSaySoWhenTheDaysCouldNotBeRead(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	v := paidView(30, map[int]int64{3: 2, 10: 1, 29: 4})
	v.Paid = PaidDays{}
	v.DailyUnavailable = true
	html := renderComponent(t, ctx, Report(layouts.Page{Title: "Reports"}, &v))
	if got := strings.Count(html, "This chart&#39;s data is unavailable right now."); got != 2 {
		t.Errorf("the page says %d times that a chart is unavailable, want once for each of the two", got)
	}
	if strings.Contains(html, "No paid orders Sep") {
		t.Error("a chart that could not be read is told as a period without orders")
	}
}

func TestDepartmentsAreDrawnAsBarsOnOneScale(t *testing.T) {
	t.Parallel()

	html := renderToString(t, Report(layouts.Page{Title: "Reports"}, &ReportView{
		Days: 30, Windows: []int32{7, 30, 90}, Placed: 3, Committed: 3, Orders: 3,
		Departments: []Department{
			{Name: "Audio", SalesCents: 400000},
			{Name: "Cables", SalesCents: 100000},
		},
	}))

	for _, want := range []string{"各館商品銷售額", "Audio", "Cables", "商品銷售額 NT$4,000", "商品銷售額 NT$1,000", `width="100.00%"`, `width="25.00%"`} {
		if !strings.Contains(html, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	if got := strings.Count(html, `class="goen-chartbar__fill"`); got != 2 {
		t.Errorf("report draws %d department bars, want 2", got)
	}
}

func TestOneDepartmentIsASentenceWithNoBar(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		locale i18n.Locale
		want   string
	}{
		{i18n.En, "All product sales in this period are in Audio: NT$4,000."},
		{i18n.ZhHant, "這段期間的商品銷售額全部屬於音響：NT$4,000。"},
	} {
		t.Run(string(tt.locale), func(t *testing.T) {
			t.Parallel()
			name := "Audio"
			if tt.locale == i18n.ZhHant {
				name = "音響"
			}
			var b bytes.Buffer
			view := ReportView{
				Days: 30, Windows: []int32{7, 30, 90}, Placed: 1, Committed: 1, Orders: 1,
				Departments: []Department{{Name: name, SalesCents: 400000}},
			}
			if err := Report(layouts.Page{Title: "Reports"}, &view).Render(i18n.WithLocale(t.Context(), tt.locale), &b); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(b.String(), tt.want) {
				t.Errorf("report lacks %q", tt.want)
			}
			if strings.Contains(b.String(), "goen-chartbar") {
				t.Error("report draws a bar for a single department")
			}
		})
	}
}

func TestNoDepartmentsShowNoDepartmentSection(t *testing.T) {
	t.Parallel()

	html := renderToString(t, Report(layouts.Page{Title: "Reports"}, &ReportView{
		Days: 30, Windows: []int32{7, 30, 90}, Placed: 1, Committed: 1, Orders: 1,
	}))
	if strings.Contains(html, "各館商品銷售額") {
		t.Error("report shows a department heading with no departments")
	}
}
