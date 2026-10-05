package admin

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
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
			Stock: 2, Safety: 4, Sold: 8, DaysCover: 7,
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
		got := ReportView{Placed: tt.placed, Committed: tt.committed}.Completion(ctx)
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

	if got := strings.Count(html, `class="goen-chart__hue"`); got != 3 {
		t.Fatalf("report shows %d bars, want 3", got)
	}
	if got := strings.Count(html, `width="80.00%"`); got != 2 {
		t.Errorf("report shows %d bars at the full reach, want the 2 tied sellers", got)
	}
	if !strings.Contains(html, `width="20.00%"`) {
		t.Error("report does not draw 10 units at a quarter of 40")
	}
	for _, label := range []string{">40</text>", ">10</text>"} {
		if !strings.Contains(html, label) {
			t.Errorf("report omits the bar label %s", label)
		}
	}
	if !strings.Contains(html, "Alpha") || !strings.Contains(html, "Gamma") {
		t.Error("report lost the product names the bars sit beside")
	}
}

func TestNoSellersDrawNoBars(t *testing.T) {
	t.Parallel()

	html := renderToString(t, Report(layouts.Page{Title: "Reports"}, &ReportView{
		Days: 30, Windows: []int32{7, 30, 90}, Placed: 1, Committed: 1, Orders: 1,
	}))
	if strings.Contains(html, "goen-chart__hue") {
		t.Error("report draws a bar with no best sellers")
	}
}
