package admin

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestTheStockListMarksItsFilterAndCarriesItsPlaceInEachForm(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := VariantsView{
		SoldOutOnly: true, Return: "/admin/stock?soldout=1&after=T",
		Variants: []Variant{{SKU: "A-1", ProductName: "x"}},
	}
	html := renderComponent(t, ctx, Variants(layouts.Page{}, view))

	// The sidebar marks the stock section itself, so only the filter bar counts.
	_, afterBar, _ := strings.Cut(html, `ui-filterbar`)
	bar, _, _ := strings.Cut(afterBar, "</div>")
	if got := strings.Count(bar, `aria-current="page"`); got != 1 {
		t.Errorf("%d filters are marked current, want 1", got)
	}
	if !strings.Contains(html, `href="/admin/stock?soldout=1" aria-current="page"`) {
		t.Error("the low-stock filter is not the one marked current")
	}
	if !strings.Contains(html, `id="row-A-1"`) {
		t.Error("the row has no anchor to return to")
	}
	if got := strings.Count(html, `name="return" value="/admin/stock?soldout=1&amp;after=T"`); got != 4 {
		t.Errorf("%d forms post their place back, want price, arrival, adjust and active", got)
	}
}

func TestARefusedAdjustmentKeepsWhatWasTypedAndMarksIt(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := VariantsView{Variants: []Variant{
		{SKU: "A-1", ProductName: "x", DraftDelta: "12x", DeltaError: "請輸入不為 0 的整數"},
		{SKU: "B-2", ProductName: "y"},
	}}
	html := renderComponent(t, ctx, Variants(layouts.Page{}, view))

	for _, want := range []string{`name="delta" value="12x"`, `aria-describedby="adj-error-A-1"`, `id="adj-error-A-1"`} {
		if !strings.Contains(html, want) {
			t.Errorf("the refused adjustment row is missing %q", want)
		}
	}
	if strings.Contains(html, "adj-error-B-2") {
		t.Error("an untouched row is marked invalid")
	}
}

func TestTheStockListSearchesAndTellsVariantsApartByTheirOptions(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := VariantsView{
		SoldOutOnly: true, Term: "koto",
		Variants: []Variant{{SKU: "KOTO-CBL-1", ProductName: "x", Options: []string{"黑", "L"}}},
	}
	html := renderComponent(t, ctx, Variants(layouts.Page{}, view))

	for _, want := range []string{
		`action="/admin/stock"`, `name="q" value="koto"`, `name="soldout" value="1"`,
		`for="stock-search"`, i18n.T(ctx, i18n.KeyAdminStockSearch),
		"黑 · L",
		`href="/admin/stock?q=koto"`, `href="/admin/stock?q=koto&amp;soldout=1"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("stock list lacks %q", want)
		}
	}
	// Every field on the row, price and adjustment alike, names itself.
	if got, want := strings.Count(html, `<input class="ui-input`), strings.Count(html, `class="goen-sr-only" for=`); got != want {
		t.Errorf("%d inputs but %d labels", got, want)
	}
}
