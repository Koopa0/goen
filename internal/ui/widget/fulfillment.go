package widget

import "github.com/koopa0/goen/internal/order"

// FundedFulfillmentIntent is the colour group of the word
// order.FulfillmentStatus.FundedLabel gives the same arguments: an order waiting
// for the customer is neutral, one waiting for the shop to pick it needs the
// staff, picking and shipping are under way, and delivered or completed is
// finished.
func FundedFulfillmentIntent(status order.FulfillmentStatus, committed bool, owedCents int64) Intent {
	switch {
	case status.ReadyToPick(committed, owedCents):
		return IntentWarn
	case status == order.FulfillmentPicking, status == order.FulfillmentShipped:
		return IntentProgress
	case status == order.FulfillmentDelivered, status == order.FulfillmentCompleted:
		return IntentDone
	default:
		return IntentNeutral
	}
}
