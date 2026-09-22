package pages

import (
	"bytes"
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

func TestBestSellerGrossCannotBeMistakenForOrderRevenue(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale              i18n.Locale
		gross, units, scope string
	}{
		{i18n.ZhHant, "商品毛額 NT$1,000", "售出 2 件", "未扣訂單折扣或退款，不含運費與訂單稅額"},
		{i18n.En, "Product gross NT$1,000", "2 units sold", "before order discounts or refunds and excluding shipping and order tax"},
	} {
		t.Run(string(tt.locale), func(t *testing.T) {
			t.Parallel()
			var b bytes.Buffer
			view := AdminReportView{Placed: 1, Orders: 1, Committed: 1, RevenueCents: 90000, Sellers: []AdminSeller{{Name: "Discounted item", Slug: "discounted", Units: 2, RevenueCents: 100000}}}
			if err := AdminReport(layouts.Page{Title: "Reports"}, &view).Render(i18n.WithLocale(t.Context(), tt.locale), &b); err != nil {
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
