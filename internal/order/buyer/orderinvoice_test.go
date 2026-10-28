package buyer

import (
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestOrderInvoicePanel: the order page shows the invoice panel only once an
// invoice is filed, with each invoice and allowance document's number, date,
// random code, amount and voided mark, and never prints a mobile barcode in
// full.
func TestOrderInvoicePanel(t *testing.T) {
	t.Parallel()
	render := func(inv *OrderInvoice) string {
		return renderToString(t, Order(frame.Page{Title: "GO-1"}, &OrderView{
			Number: "GO-260929-000001", Status: order.FulfillmentPicking, Committed: true, Invoice: inv,
		}))
	}

	if html := render(nil); strings.Contains(html, "invoice-heading") {
		t.Error("an order with no invoice shows an invoice panel")
	}

	html := render(&OrderInvoice{
		Type: invoice.PreferenceMobile, MobileBarcode: "/ABC1234",
		Documents: []OrderInvoiceDocument{
			{Number: "AB12345678", RandomCode: "4321", IssuedOn: shoptime.Date{Year: 2026, Month: time.October, Day: 1}, Voided: true},
			{Number: "AB12345679", RandomCode: "8765", IssuedOn: shoptime.Date{Year: 2026, Month: time.October, Day: 2}},
			{Allowance: true, Number: "CD00000001", AmountCents: 30000, IssuedOn: shoptime.Date{Year: 2025, Month: time.December, Day: 5, OtherYear: true}},
		},
	})
	for _, want := range []string{"AB12345678", "AB12345679", "10\u00a0月 2\u00a0日 開立", "2025\u00a0年 12\u00a0月 5\u00a0日 開立", "8765", "CD00000001", "NT$300", "（已作廢）", "/*****34"} {
		if !strings.Contains(html, want) {
			t.Errorf("the invoice panel is missing %q", want)
		}
	}
	if strings.Contains(html, "2026-10") || strings.Contains(html, "2025-12") {
		t.Error("an invoice date is printed as an ISO date")
	}
	if strings.Contains(html, "/ABC1234") {
		t.Error("the mobile barcode is printed in full")
	}
}
