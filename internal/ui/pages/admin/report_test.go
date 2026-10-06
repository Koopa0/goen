package admin

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
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
			Sellable: 2, Sold: 8, DaysCover: 7,
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

func TestDepartmentsAreDrawnAsBarsOnOneScale(t *testing.T) {
	t.Parallel()

	html := renderToString(t, Report(layouts.Page{Title: "Reports"}, &ReportView{
		Days: 30, Windows: []int32{7, 30, 90}, Placed: 3, Committed: 3, Orders: 3,
		Departments: []Department{
			{Name: "Audio", RevenueCents: 400000},
			{Name: "Cables", RevenueCents: 100000},
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
				Departments: []Department{{Name: name, RevenueCents: 400000}},
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
