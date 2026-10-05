package admin

import (
	"slices"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/ui/layouts"
)

// TestARefundedOrderCanFileAnAllowance holds the 折讓 form's door. Without it a
// customer is refunded while the 統一發票 still records the whole sale.
func TestARefundedOrderCanFileAnAllowance(t *testing.T) {
	t.Parallel()

	refunded := OrderView{
		Number: "GO-260721-000387", Status: "completed",
		Committed: true, InvoicingEnabled: true, RefundedCents: 84900,
		InvoiceDocuments: []InvoiceDocument{
			{Kind: "invoice", Number: "AA12345678", Status: "issued", AmountCents: 100000},
		},
	}
	html := renderToString(t, Order(layouts.Page{Title: "x"}, &refunded))

	if !strings.Contains(html, `action="/admin/orders/GO-260721-000387/invoice/allowance"`) {
		t.Error("a refunded order offers no way to file a 折讓, so the tax document " +
			"keeps recording a sale that partly did not happen")
	}
	// Displayed from what actually went back, but never posted: the database
	// derives it again under lock, so neither an operator nor a forged form owns
	// the tax amount.
	if !strings.Contains(html, `NT$849`) {
		t.Error("the allowance form does not show the authoritative refunded delta")
	}
	if strings.Contains(html, `name="amount"`) {
		t.Error("the allowance form posts an operator-controlled money field")
	}

	// And it is not offered when nothing has been refunded — an allowance
	// relieving nothing is refused downstream, and the form would be an
	// invitation to invent a figure.
	nothingBack := refunded
	nothingBack.RefundedCents = 0
	if strings.Contains(renderToString(t, Order(layouts.Page{Title: "x"}, &nothingBack)),
		"/invoice/allowance") {
		t.Error("an order with no refund is offered a 折讓 form")
	}

	partlyRelieved := refunded
	partlyRelieved.InvoiceDocuments = append(
		slices.Clone(refunded.InvoiceDocuments),
		InvoiceDocument{Kind: "allowance", Number: "2026080715227214",
			Status: "issued", AmountCents: 30000},
	)
	partialHTML := renderToString(t, Order(layouts.Page{Title: "x"}, &partlyRelieved))
	if !strings.Contains(partialHTML, `NT$549`) {
		t.Error("the allowance form did not subtract the credit note already filed")
	}
}

func TestAnIssuedInvoiceShowsWithoutTheInvoiceService(t *testing.T) {
	t.Parallel()

	view := OrderView{
		Number: "GO-260721-000387", Status: "completed", Committed: true, RefundedCents: 84900,
		InvoiceDocuments: []InvoiceDocument{
			{Kind: "invoice", Number: "LC97535645", Status: "issued", AmountCents: 100000},
		},
	}
	html := renderToString(t, Order(layouts.Page{Title: "x"}, &view))

	if !strings.Contains(html, "LC97535645") {
		t.Error("an order with an issued invoice hides it while e-invoicing is off")
	}
	for _, action := range []string{"/invoice\"", "/invoice/void", "/invoice/allowance"} {
		if strings.Contains(html, action) {
			t.Errorf("e-invoicing is off but the page offers the action %s", action)
		}
	}
}
