// Package orderstatus is where an order stands as the back-office desks show
// it; it holds Badge, the word and colour group of an order's status badge;
// it does not hold the states or their words (order) or the storefront's own
// badge (account).
package orderstatus

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/widget"
)

// Badge is the word and colour an order carries on the orders queue, the
// dashboard and a customer's page: an order returned in full, with every refund
// settled, says so in place of where its delivery stands. committed and
// owedCents are what FundedLabel reads.
func Badge(ctx context.Context, s order.FulfillmentStatus, committed bool, owedCents int64, returned bool) (string, widget.Intent) {
	if returned {
		return i18n.T(ctx, i18n.KeyStatusRefunded), widget.IntentNeutral
	}
	return s.FundedLabel(ctx, committed, owedCents), intent(s, committed, owedCents)
}

// intent is the colour group of the word s.FundedLabel gives the same
// arguments: an order waiting for the customer is neutral, one waiting for the
// shop to pack it is the staff's turn, packing and shipping are under way, and
// delivered or completed is finished.
func intent(s order.FulfillmentStatus, committed bool, owedCents int64) widget.Intent {
	switch {
	case s.ReadyToPick(committed, owedCents):
		return widget.IntentStaffTurn
	case s == order.FulfillmentPicking, s == order.FulfillmentShipped:
		return widget.IntentProgress
	case s == order.FulfillmentDelivered, s == order.FulfillmentCompleted:
		return widget.IntentDone
	default:
		return widget.IntentNeutral
	}
}
