package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

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
