package checkout

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
)

// TestPageCompanyInvoiceStored: Page shows the note saying where a company
// invoice is stored inside the company card and in no other, in each locale.
func TestPageCompanyInvoiceStored(t *testing.T) {
	t.Parallel()
	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		v := View{
			Cart:           cart.View{Lines: []cart.Line{{Name: "x", Quantity: 1, UnitCents: 100}}},
			Shipping:       []cart.ShippingChoice{{VersionID: "ship-1", Code: "home", Name: "home"}},
			Chosen:         "ship-1",
			InvoiceChoices: invoiceChoices(ctx),
		}
		doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, Page(Meta(ctx), &v))))
		if err != nil {
			t.Fatal(err)
		}
		want := i18n.T(ctx, i18n.KeyInvoiceCompanyStored)
		for _, kind := range invoice.OfferedPreferences() {
			got := strings.Contains(nodeText(invoiceCard(t, doc, kind)), want)
			if got != (kind == invoice.PreferenceCompany) {
				t.Errorf("%v: the %s card shows where a company invoice is stored = %v", loc, kind, got)
			}
		}
	}
}
