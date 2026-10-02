package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// The order page shows the invoice once it is filed and nothing before, and
// never prints a mobile carrier in full.
func TestTheOrderPageShowsTheInvoiceOnlyOnceFiled(t *testing.T) {
	t.Parallel()
	render := func(inv *OrderInvoice) string {
		return renderToString(t, Order(layouts.Page{Title: "GO-1"}, &OrderView{
			Number: "GO-260929-000001", Status: FulfillmentPicking, Committed: true, Invoice: inv,
		}))
	}

	if html := render(nil); strings.Contains(html, "invoice-heading") {
		t.Error("an order with no invoice shows an invoice panel")
	}

	html := render(&OrderInvoice{
		Type: invoice.PreferenceMobile, Carrier: "/ABC1234",
		Documents: []OrderInvoiceDocument{
			{Number: "AB12345678", RandomCode: "4321", IssuedOn: "2026-10-01", Voided: true},
			{Number: "AB12345679", RandomCode: "8765", IssuedOn: "2026-10-02"},
			{Allowance: true, Number: "CD00000001", AmountCents: 30000, IssuedOn: "2026-10-05"},
		},
	})
	for _, want := range []string{"AB12345678", "AB12345679", "2026-10-02", "8765", "CD00000001", "NT$300", "（已作廢）", "/*****34"} {
		if !strings.Contains(html, want) {
			t.Errorf("the invoice panel is missing %q", want)
		}
	}
	if strings.Contains(html, "/ABC1234") {
		t.Error("the mobile carrier is printed in full")
	}
}
