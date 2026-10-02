package admin

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
)

func TestProductInvoiceFormRetainsRefusedTaxAndUnit(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		facts := invoice.ProductFacts{TaxType: "zero_rated", Unit: "<unit>"}
		v := ProductView{Slug: "p", InvoiceFacts: &facts, Errors: map[string]string{"tax_type": "Tax error", "invoice_unit": "Unit error"}}
		var body strings.Builder
		if err := productInvoice(v).Render(ctx, &body); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`method="post"`, `action="/admin/products/p/invoice"`, `value="zero_rated" selected`, `value="&lt;unit&gt;"`, `maxlength="6"`, `list="invoice-units"`, `aria-invalid="true"`, `invoice-unit-error`, i18n.T(ctx, i18n.KeyInvoiceTaxType)} {
			if !strings.Contains(body.String(), want) {
				t.Errorf("%s missing %q", locale, want)
			}
		}
		if !strings.Contains(body.String(), `value="包"`) {
			t.Error("invoice unit suggestions were translated or lost")
		}
	}
}
