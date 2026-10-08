package admin

import (
	"errors"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func renderEnglish(t *testing.T, c templ.Component) string {
	t.Helper()
	return renderComponent(t, i18n.WithLocale(t.Context(), i18n.En), c)
}

func returnedReport(rows ...ReturnedProduct) *ReportView {
	return &ReportView{
		Days: 30, Windows: []int32{7, 30, 90}, Placed: 3, Committed: 3, Orders: 3,
		Returned: rows,
	}
}

func TestProductsReturnedMostShowCountsBelowTwentySold(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		row  ReturnedProduct
		want string
		not  string
	}{
		{"19 sold is a count", ReturnedProduct{Name: "A", Returned: 3, Sold: 19}, "3 / 19 units", "% returned"},
		{"20 sold is a rate", ReturnedProduct{Name: "A", Returned: 3, Sold: 20}, "15% returned", ""},
		{"a rate rounds half up", ReturnedProduct{Name: "A", Returned: 1, Sold: 40}, "3% returned", ""},
		{"a rate is never 0% while any came back", ReturnedProduct{Name: "A", Returned: 1, Sold: 1000}, "1% returned", ""},
		{"a rate is never 100% unless all came back", ReturnedProduct{Name: "A", Returned: 999, Sold: 1000}, "99% returned", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			other := ReturnedProduct{Slug: "z", Name: "Z", Returned: 1, Sold: 5}
			html := renderEnglish(t, Report(layouts.Page{Title: "Reports"}, returnedReport(tt.row, other)))
			if !strings.Contains(html, tt.want) {
				t.Errorf("Report with %+v does not say %q", tt.row, tt.want)
			}
			if tt.not != "" {
				_, row, _ := strings.Cut(html, ">A<")
				row, _, _ = strings.Cut(row, "</li>")
				if strings.Contains(row, tt.not) {
					t.Errorf("Report with %+v shows %q under 20 sold: %s", tt.row, tt.not, row)
				}
			}
		})
	}
}

func TestProductsReturnedMostRateKeepsTheCountsToo(t *testing.T) {
	t.Parallel()

	html := renderEnglish(t, Report(layouts.Page{Title: "Reports"}, returnedReport(
		ReturnedProduct{Slug: "a", Name: "Alpha", Returned: 5, Sold: 20},
		ReturnedProduct{Slug: "b", Name: "Beta", Returned: 2, Sold: 8},
	)))
	if !strings.Contains(html, "25% returned") || !strings.Contains(html, "5 / 20 units") {
		t.Error("a product with 20 sold must show its rate and the counts under it")
	}
}

func TestProductsReturnedMostBarIsTheRate(t *testing.T) {
	t.Parallel()

	html := renderEnglish(t, Report(layouts.Page{Title: "Reports"}, returnedReport(
		ReturnedProduct{Slug: "a", Name: "Alpha", Returned: 250, Sold: 1000},
		ReturnedProduct{Slug: "b", Name: "Beta", Returned: 1, Sold: 4},
		ReturnedProduct{Slug: "c", Name: "Gamma", Returned: 5, Sold: 20},
	)))
	_, section, _ := strings.Cut(html, "Products returned most")
	if got := strings.Count(section, `class="goen-chartbar__fill"`); got != 2 {
		t.Fatalf("section draws %d bars, want 2: the one with 4 sold shows a count, not a rate", got)
	}
	if got := strings.Count(section, `width="25.00%"`); got != 2 {
		t.Errorf("section draws %d bars at 25%%, want 2: 250 of 1000 and 5 of 20", got)
	}
	if !strings.Contains(section, `<span class="goen-chartbar__label"></span>`) {
		t.Error("section lost the empty count column: the count is already in each row's figure")
	}
}

func TestProductsReturnedMostWithNoRowsSaysNothing(t *testing.T) {
	t.Parallel()

	html := renderEnglish(t, Report(layouts.Page{Title: "Reports"}, returnedReport()))
	if strings.Contains(html, "Products returned most") {
		t.Error("a report with no returned units shows the returns section")
	}
}

func TestProductsReturnedMostWithOneRowIsASentence(t *testing.T) {
	t.Parallel()

	html := renderEnglish(t, Report(layouts.Page{Title: "Reports"}, returnedReport(
		ReturnedProduct{Slug: "a", Name: "Alpha", Returned: 2, Sold: 12},
	)))
	if !strings.Contains(html, "Only one product had returns: Alpha, 2 / 12 units.") {
		t.Error("one returned product is not stated as a sentence")
	}
	_, section, _ := strings.Cut(html, "Products returned most")
	section, _, _ = strings.Cut(section, "Stock about to run out")
	if strings.Contains(section, "goen-chartbar") {
		t.Error("one returned product draws a bar, which compares it with nothing")
	}
}

func TestProductsReturnedMostSaysWhenItCouldNotBeRead(t *testing.T) {
	t.Parallel()

	v := returnedReport(ReturnedProduct{Slug: "a", Name: "Alpha", Returned: 2, Sold: 12})
	v.ReturnedErr = errors.New("read")
	html := renderEnglish(t, Report(layouts.Page{Title: "Reports"}, v))
	if !strings.Contains(html, "Returns data is unavailable right now.") {
		t.Error("a failed read does not say so")
	}
	if strings.Contains(html, "Alpha") {
		t.Error("a failed read still lists products")
	}
	if !strings.Contains(html, "Best sellers") {
		t.Error("a failed returns read took the rest of the report with it")
	}
}

func TestProductsReturnedMostInChinese(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	p := ReturnedProduct{Name: "Alpha", Returned: 3, Sold: 12}
	if got, want := p.Counts(ctx), "3 / 12 件"; got != want {
		t.Errorf("Counts(zh) = %q, want %q", got, want)
	}
	if got, want := (ReturnedProduct{Returned: 5, Sold: 20}).Share(ctx), "25% 退貨"; got != want {
		t.Errorf("Share(zh) = %q, want %q", got, want)
	}
}
