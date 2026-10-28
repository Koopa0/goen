package reports

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/product/inventory"
	"github.com/koopa0/goen/internal/sales"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/stockrisk"
)

// The tests in this file render the report.

const stockedAllWindow = 30 * 24 * time.Hour

// TestReportStockRows: stock rows carry their range and window in text.
//
// From stockrisk_test.go, moved whole.
func TestReportStockRows(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	markup := renderComponent(t, ctx, Report(frame.Page{Title: "Reports"}, &View{
		Days: 7, StockDays: 30, Windows: []int32{7, 30, 90},
		AtRisk: []stockrisk.Row{{Risk: inventory.Risk{SKU: "OUT-1", Name: "Gone", Slug: "gone"}},
			{Risk: inventory.Risk{SKU: "EST-1", Name: "Going", Slug: "going", Sales: inventory.Sales{Sellable: 20, Sold: 30, Orders: 15, InStock: stockedAllWindow}}},
			{Risk: inventory.Risk{SKU: "FEW-1", Name: "Rare", Slug: "rare", Sales: inventory.Sales{Sellable: 9, Sold: 2, Orders: 2, InStock: stockedAllWindow}}},
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
		if !strings.Contains(markup, want) {
			t.Errorf("the stock rows lack %q", want)
		}
	}
	if got := strings.Count(markup, `<div class="goen-chartrangebar`); got != 1 {
		t.Errorf("%d range bars drawn, want 1: only the estimated row has one", got)
	}
	if strings.Contains(markup, "style=") {
		t.Error("the stock rows carry a style attribute")
	}
}

// TestReportMoreSoldOut: more sold out links to the stock desk filter.
//
// From stockrisk_test.go, moved whole.
func TestReportMoreSoldOut(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := &View{
		Days: 30, StockDays: 30, Windows: []int32{7, 30, 90}, MoreSoldOut: 2,
		AtRisk: []stockrisk.Row{{Risk: inventory.Risk{SKU: "OUT-1", Name: "Gone", Slug: "gone"}}},
	}
	markup := renderComponent(t, ctx, Report(frame.Page{Title: "Reports"}, view))
	if want := `<a href="/admin/stock?soldout=1">2 more items sold out</a>`; !strings.Contains(markup, want) {
		t.Errorf("the stock section lacks %s", want)
	}
	view.MoreSoldOut = 0
	if markup := renderComponent(t, ctx, Report(frame.Page{Title: "Reports"}, view)); strings.Contains(markup, "/admin/stock?soldout=1") {
		t.Error("the stock section links to the sold out filter with none left off")
	}
}

// TestReportSharedCoverSection: report and stock desk share the days cover
// section.
//
// From stockrisk_test.go, moved whole.
//
// The report and the stock desk draw the section from one component.
func TestReportSharedCoverSection(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	rows := []stockrisk.Row{{Risk: inventory.Risk{SKU: "OUT-1", Name: "Gone", Slug: "gone"}}}
	section := renderComponent(t, ctx, stockrisk.Section(rows, 0, inventory.CoverWindowDays))
	for name, page := range map[string]string{
		"report": renderComponent(t, ctx, Report(frame.Page{}, &View{Days: 30, Windows: []int32{7, 30, 90}, AtRisk: rows, StockDays: inventory.CoverWindowDays})),
	} {
		if !strings.Contains(page, section) {
			t.Errorf("%s does not contain the shared days cover section", name)
		}
	}
}

// TestReportListSkeleton: lists share one row skeleton.
//
// From stockrisk_test.go, moved whole.
func TestReportListSkeleton(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	stock := renderComponent(t, ctx, stockrisk.Rows([]stockrisk.Row{{Risk: inventory.Risk{SKU: "EST-1", Name: "Going", Slug: "going", Sales: inventory.Sales{Sellable: 14, Sold: 43, Orders: 36, InStock: stockedAllWindow}}}}, 0, 30))
	report := renderComponent(t, ctx, Report(frame.Page{Title: "Reports"}, &View{
		Days: 30, Windows: []int32{7, 30, 90}, Placed: 3, Committed: 3, Orders: 3,
		BestSellers: []sales.BestSeller{{Slug: "a", Name: "Alpha", Brand: "Aurora", Units: 40}, {Slug: "b", Name: "Beta", Units: 10}},
	}))
	for name, markup := range map[string]string{"stock": stock, "best sellers": report} {
		for _, want := range []string{
			`<li class="goen-report__row"><div class="goen-report__what"><a class="goen-report__name" href="/admin/products/`,
			`<p class="goen-report__facts">`,
			`<div class="goen-report__plot">`,
		} {
			if !strings.Contains(markup, want) {
				t.Errorf("%s list lacks the shared row markup %s", name, want)
			}
		}
	}
}

// TestReturnedProductsTrackScale: returned rate bars share one track scale.
//
// From reviewfollowups_test.go, moved whole.
func TestReturnedProductsTrackScale(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		view := returnedReport(
			ReturnedProduct{Name: "Large", Returned: 250, Sold: 1000},
			ReturnedProduct{Name: "Small", Returned: 5, Sold: 20},
			ReturnedProduct{Name: "Rare", Returned: 1, Sold: 100000},
			ReturnedProduct{Name: "Too few", Returned: 1, Sold: 4},
		)
		doc := reviewFollowupDocument(t, renderComponent(t, ctx, returnedProducts(view)))
		var classes, widths []string
		for node := range doc.Descendants() {
			if node.Data == "div" && strings.HasPrefix(attr(node, "class"), "goen-chartbar") {
				classes = append(classes, attr(node, "class"))
			}
			if node.Data == "rect" && attr(node, "class") == "goen-chartbar__fill" {
				widths = append(widths, attr(node, "width"))
			}
		}
		if len(classes) != 3 {
			t.Fatalf("%s: rate bars = %d, want three", locale, len(classes))
		}
		for _, class := range classes[1:] {
			if class != classes[0] {
				t.Errorf("%s: rate bars use different track classes %q and %q", locale, classes[0], class)
			}
		}
		if diff := cmp.Diff([]string{"25.00%", "25.00%", "0.60%"}, widths); diff != "" {
			t.Errorf("%s: rate widths (-want +got):\n%s", locale, diff)
		}
	}
}

func reviewFollowupDocument(t *testing.T, markup string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
