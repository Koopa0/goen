package pages

import (
	"fmt"
	"html"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestReportExportKeepsWindowAndExplainsGrossAmount(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, days := range []int{7, 30, 90} {
			ctx := i18n.WithLocale(t.Context(), locale)
			var output strings.Builder
			if err := AdminReport(layouts.Page{}, &AdminReportView{Days: days}).Render(ctx, &output); err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf(`href="/admin/reports/export.csv?days=%d"`, days)
			if !strings.Contains(output.String(), want) {
				t.Fatalf("empty report lost selected-window export: %s", want)
			}
			for _, key := range []i18n.Key{i18n.KeyAdminRepExport, i18n.KeyAdminRepExportHint} {
				if !strings.Contains(html.UnescapeString(output.String()), i18n.T(ctx, key)) {
					t.Fatalf("report lacks localized export scope: %s", key)
				}
			}
		}
	}
}

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
