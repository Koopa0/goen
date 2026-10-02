package pages

import (
	"html"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	invoicepkg "github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestCompanyInvoiceChoiceSaysWhereTheInvoiceIsStored(t *testing.T) {
	t.Parallel()
	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		view := func(typ string) string {
			v := CheckoutView{
				Cart:           CartView{Lines: []CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
				Shipping:       []ShippingChoice{{VersionID: "ship-1", Code: "home", Name: "home"}},
				Chosen:         "ship-1",
				Invoice:        CheckoutInvoice{Type: invoicepkg.Preference(typ)},
				InvoiceChoices: []InvoiceChoice{{Value: "company", Label: "company"}, {Value: "mobile_carrier", Label: "mobile"}},
			}
			var b strings.Builder
			if err := Checkout(CheckoutMeta(ctx), &v).Render(ctx, &b); err != nil {
				t.Fatal(err)
			}
			return b.String()
		}
		want := html.EscapeString(i18n.T(ctx, i18n.KeyInvoiceCompanyStored))
		if !strings.Contains(view("company"), want) {
			t.Errorf("%v: the company invoice choice does not say where the invoice is stored", loc)
		}
		if strings.Contains(view("mobile_carrier"), want) {
			t.Errorf("%v: the mobile-barcode choice shows the company-invoice storage note", loc)
		}
	}
}

func TestThePointsPageSaysWhatExpiresAndWhatDoesNot(t *testing.T) {
	t.Parallel()
	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		var b strings.Builder
		if err := Points(layouts.Page{Title: "p"}, PointsView{PerCredit: 1}).Render(ctx, &b); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), html.EscapeString(i18n.T(ctx, i18n.KeyPointsExpiryTerms))) {
			t.Errorf("%v: the points page does not state the expiry rule", loc)
		}
	}
}
