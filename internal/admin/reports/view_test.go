package reports

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	xhtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/inventory"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

// The tests in this file render the report and moved whole from test files
// that still live in internal/ui/pages/admin, as in #2082 and #2129. That
// package's test build cannot import this one, because this package still
// imports it for the days cover rows and section the report, the stock desk
// and the dashboard share.

const stockedAllWindow = 30 * 24 * time.Hour

// From stockrisk_test.go, moved whole.
func TestStockRowsCarryTheirRangeAndWindowInText(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	html := renderComponent(t, ctx, Report(layouts.Page{Title: "Reports"}, &ReportView{
		Days: 7, StockDays: 30, Windows: []int32{7, 30, 90},
		AtRisk: []admin.StockRisk{{Risk: inventory.Risk{SKU: "OUT-1", Name: "Gone", Slug: "gone"}},
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

// From stockrisk_test.go, moved whole.
func TestMoreSoldOutLinksToTheStockDeskFilter(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := &ReportView{
		Days: 30, StockDays: 30, Windows: []int32{7, 30, 90}, MoreSoldOut: 2,
		AtRisk: []admin.StockRisk{{Risk: inventory.Risk{SKU: "OUT-1", Name: "Gone", Slug: "gone"}}},
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

// From stockrisk_test.go, moved whole.
//
// The report and the stock desk draw the section from one component.
func TestReportAndStockDeskShareTheDaysCoverSection(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	rows := []admin.StockRisk{{Risk: inventory.Risk{SKU: "OUT-1", Name: "Gone", Slug: "gone"}}}
	section := renderComponent(t, ctx, admin.StockRiskSection(rows, 0, inventory.CoverWindowDays))
	for name, page := range map[string]string{
		"report": renderComponent(t, ctx, Report(layouts.Page{}, &ReportView{Days: 30, Windows: []int32{7, 30, 90}, AtRisk: rows, StockDays: inventory.CoverWindowDays})),
	} {
		if !strings.Contains(page, section) {
			t.Errorf("%s does not contain the shared days cover section", name)
		}
	}
}

// From stockrisk_test.go, moved whole.
func TestListsShareOneRowSkeleton(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	stock := renderComponent(t, ctx, admin.StockRiskRows([]admin.StockRisk{{Risk: inventory.Risk{SKU: "EST-1", Name: "Going", Slug: "going", Sales: inventory.Sales{Sellable: 14, Sold: 43, Orders: 36, InStock: stockedAllWindow}}}}, 0, 30))
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

// From stockrisk_test.go, moved whole.
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

// From reviewfollowups_test.go, moved whole.
func TestReturnedRateBarsShareOneTrackScale(t *testing.T) {
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

func reviewFollowupDocument(t *testing.T, markup string) *xhtml.Node {
	t.Helper()
	doc, err := xhtml.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
