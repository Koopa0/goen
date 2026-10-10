package pages

import (
	"strings"
	"testing"

	htmlparse "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestCartMarksOnlyExemptLines(t *testing.T) {
	t.Parallel()
	for _, locale := range []struct {
		locale i18n.Locale
		marker string
	}{
		{locale: i18n.ZhHant, marker: "免稅"},
		{locale: i18n.En, marker: "Exempt"},
	} {
		t.Run(string(locale.locale), func(t *testing.T) {
			t.Parallel()
			for _, mixed := range []bool{true, false} {
				ctx := i18n.WithLocale(t.Context(), locale.locale)
				cart := CartView{MixedTaxTypes: mixed, Lines: []CartLine{
					{VariantID: "exempt", Name: "Exempt item", TaxExempt: true, Quantity: 1, Available: 1},
					{VariantID: "exempt-short", Name: "Short item", TaxExempt: true, Quantity: 2, Available: 1, Short: true},
					{VariantID: "exempt-unavailable", Name: "Unavailable item", TaxExempt: true, Quantity: 1, Unavailable: true},
				}}
				if mixed {
					cart.Lines = append(cart.Lines, CartLine{VariantID: "taxable", Name: "Taxable item", Quantity: 1, Available: 1})
				}
				rendered := renderComponent(t, ctx, Cart(CartMeta(ctx), cart))
				doc, err := htmlparse.Parse(strings.NewReader(rendered))
				if err != nil {
					t.Fatal(err)
				}
				markers := make(map[string]int)
				for n := range doc.Descendants() {
					if n.Type != htmlparse.ElementNode || n.Data != "li" {
						continue
					}
					var id string
					for _, a := range n.Attr {
						if a.Key == "id" {
							id = a.Val
						}
					}
					if !strings.HasPrefix(id, "line-") {
						continue
					}
					markers[id] = 0
					for child := range n.Descendants() {
						if child.Type == htmlparse.TextNode && strings.TrimSpace(child.Data) == locale.marker {
							markers[id]++
						}
					}
				}
				if len(markers) != len(cart.Lines) {
					t.Fatalf("rendered %d cart lines, want %d", len(markers), len(cart.Lines))
				}
				for _, line := range cart.Lines {
					want := 0
					if line.TaxExempt {
						want = 1
					}
					if got := markers["line-"+line.VariantID]; got != want {
						t.Errorf("mixed=%t line %s has %d exempt markers, want %d", mixed, line.VariantID, got, want)
					}
				}
			}
		})
	}
}

func TestMixedTaxCartExplainsWhyCheckoutIsUnavailable(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			cart := CartView{Lines: []CartLine{{Name: "Item", Quantity: 1, Available: 1, UnitCents: 100}}, MixedTaxTypes: true}
			if cart.CanCheckout() {
				t.Error("mixed taxable and exempt cart can check out")
			}
			rendered := renderComponent(t, ctx, Cart(layouts.Page{Title: "Cart"}, cart))
			if !strings.Contains(rendered, i18n.T(ctx, i18n.KeyCartMixedTaxTypes)) {
				t.Error("mixed cart has no explanation")
			}
			if strings.Contains(rendered, `href="/checkout"`) || !strings.Contains(rendered, `aria-disabled="true"`) {
				t.Error("mixed cart still offers checkout")
			}
			cart.MixedTaxTypes = false
			if !cart.CanCheckout() {
				t.Error("one tax type cannot check out")
			}
			rendered = renderComponent(t, ctx, Cart(layouts.Page{Title: "Cart"}, cart))
			if !strings.Contains(rendered, `href="/checkout"`) || strings.Contains(rendered, i18n.T(ctx, i18n.KeyCartMixedTaxTypes)) {
				t.Error("single-tax cart did not recover checkout")
			}
		})
	}
}
