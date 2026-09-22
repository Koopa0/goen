package pages

import "testing"

func TestCancelledOrderDoesNotOfferInvoiceIssue(t *testing.T) {
	t.Parallel()
	v := AdminOrderView{InvoicingEnabled: true, Committed: true, Status: FulfillmentPending}
	if !v.CanIssueInvoice() {
		t.Fatal("committed open order cannot issue")
	}
	v.Status = FulfillmentCancelled
	if v.CanIssueInvoice() {
		t.Fatal("cancelled order offers a new invoice")
	}
}
