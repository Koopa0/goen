package stock

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	nethtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestVariantsFilterAndPlace: the stock list marks its filter and carries its
// place in each form.
func TestVariantsFilterAndPlace(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := VariantsView{
		SoldOutOnly: true, Return: "/admin/stock?soldout=1&after=T",
		Variants: []Variant{{SKU: "A-1", ProductName: "x"}},
	}
	html := renderComponent(t, ctx, Variants(frame.Page{}, view))

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
	if got := strings.Count(html, `name="return" value="/admin/stock?soldout=1&amp;after=T"`); got != 3 {
		t.Errorf("%d forms post their place back, want price, arrival and receive", got)
	}
}

// TestVariantsRefusedReceipt: a refused receipt keeps what was typed on its row
// and marks it.
func TestVariantsRefusedReceipt(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := VariantsView{Variants: []Variant{
		{SKU: "A-1", ProductName: "x", DraftQuantity: "12x", QuantityError: "請輸入 1 到 10,000 的整數"},
		{SKU: "B-2", ProductName: "y"},
	}}
	html := renderComponent(t, ctx, Variants(frame.Page{}, view))

	for _, want := range []string{`name="quantity" value="12x"`, `aria-describedby="rcv-A-1-error"`, `id="rcv-A-1-error"`} {
		if !strings.Contains(html, want) {
			t.Errorf("the refused receipt row is missing %q", want)
		}
	}
	if strings.Contains(html, "rcv-B-2-error") {
		t.Error("an untouched row is marked invalid")
	}
}

// TestVariantsRowOnlyReceives: a row posts the goods receipt and neither of the
// two writes that moved to the SKU page.
func TestVariantsRowOnlyReceives(t *testing.T) {
	t.Parallel()
	for _, active := range []bool{true, false} {
		t.Run(fmt.Sprintf("active=%t", active), func(t *testing.T) {
			t.Parallel()
			row := Variant{SKU: "A-1", ProductName: "x", Active: active, FormID: "form-1"}
			markup := renderComponent(t, i18n.WithLocale(t.Context(), i18n.En), Variants(frame.Page{}, VariantsView{Variants: []Variant{row}}))
			doc, err := nethtml.Parse(strings.NewReader(markup))
			if err != nil {
				t.Fatal(err)
			}
			var actions []string
			var receive *nethtml.Node
			for n := range doc.Descendants() {
				if n.Type == nethtml.ElementNode && n.Data == "form" && strings.HasPrefix(attr(n, "action"), "/admin/stock/") {
					actions = append(actions, attr(n, "action"))
					if strings.HasPrefix(attr(n, "action"), "/admin/stock/receive") {
						receive = n
					}
				}
			}
			want := []string{"/admin/stock/price", "/admin/stock/arrival", "/admin/stock/receive#row-A-1"}
			if diff := cmp.Diff(want, actions); diff != "" {
				t.Errorf("the row's forms (-want +got):\n%s", diff)
			}
			if receive == nil {
				t.Fatal("the row has no receive form")
			}
			values := map[string]string{}
			for n := range receive.Descendants() {
				if n.Type == nethtml.ElementNode && n.Data == "input" {
					values[attr(n, "name")] = attr(n, "value")
				}
			}
			if values["idempotency"] != "rcv:A-1:form-1" || values["sku"] != "A-1" {
				t.Errorf("receive form sku/idempotency = %q/%q, want A-1 and rcv:A-1:form-1", values["sku"], values["idempotency"])
			}
		})
	}
}

// TestVariantsSellableCell: the sellable count leads its cell, and a row at its
// safety stock says so in words.
func TestVariantsSellableCell(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		low := renderComponent(t, ctx, Variants(frame.Page{}, VariantsView{Variants: []Variant{{SKU: "A-1", ProductName: "x", Stock: 2, Safety: 2}}}))
		fine := renderComponent(t, ctx, Variants(frame.Page{}, VariantsView{Variants: []Variant{{SKU: "A-1", ProductName: "x", Stock: 6, Safety: 2}}}))
		facts := fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminStockFacts), "6", "2")
		if !strings.Contains(fine, `<span class="goen-admin__sellable">4</span>`) || !strings.Contains(fine, facts) {
			t.Errorf("%s: a row with 6 in stock and safety 2 lacks sellable 4 and %q", locale.Tag(), facts)
		}
		if strings.Contains(fine, i18n.T(ctx, i18n.KeyAdminStockLow)) {
			t.Errorf("%s: a row above its safety stock says it is low", locale.Tag())
		}
		if !strings.Contains(low, i18n.T(ctx, i18n.KeyAdminStockLow)) {
			t.Errorf("%s: a row at its safety stock does not say it is low", locale.Tag())
		}
	}
}

// TestVariantsSearchAndOptions: the stock list searches and tells variants apart
// by their options.
func TestVariantsSearchAndOptions(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := VariantsView{
		SoldOutOnly: true, Term: "koto",
		Variants: []Variant{{SKU: "KOTO-CBL-1", ProductName: "x", Options: []string{"黑", "L"}}},
	}
	html := renderComponent(t, ctx, Variants(frame.Page{}, view))

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
	// Every field on the row, price and receipt alike, names itself.
	if got, want := strings.Count(html, `<input class="ui-input`), len(regexp.MustCompile(`<label [^>]*for=`).FindAllString(html, -1)); got != want {
		t.Errorf("%d inputs but %d labels", got, want)
	}
}
