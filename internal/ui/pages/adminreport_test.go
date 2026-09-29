package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestAnEmptySalesWindowStillListsStockAtRisk(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, AdminReport(layouts.Page{Title: "報表"}, &AdminReportView{
		Days:    30,
		Windows: []int32{7, 30, 90},
		AtRisk: []AdminStockRisk{{
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
		view      AdminReportView
		wantEmpty bool
	}{
		{"orders and revenue without refunds", AdminReportView{
			Days: 7, Windows: windows, Placed: 1, Committed: 1, Orders: 1, RevenueCents: 1000,
		}, false},
		{"refunds without orders", AdminReportView{
			Days: 7, Windows: windows, RefundedCents: 12500,
		}, false},
		{"neither orders nor refunds", AdminReportView{
			Days: 7, Windows: windows,
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.view.Empty(); got != tc.wantEmpty {
				t.Errorf("Empty() = %v, want %v", got, tc.wantEmpty)
			}
			html := renderToString(t, AdminReport(layouts.Page{Title: "報表"}, &tc.view))
			if got := strings.Contains(html, empty); got != tc.wantEmpty {
				t.Errorf("no-orders state rendered = %v, want %v", got, tc.wantEmpty)
			}
			if got := strings.Contains(html, `class="goen-report__figures"`); got == tc.wantEmpty {
				t.Errorf("revenue strip rendered = %v, want %v", got, !tc.wantEmpty)
			}
		})
	}
}
