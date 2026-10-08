package admin

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/components"
)

// fulfillmentLabels is the fulfillment lifecycle, in the order the queue shows it.
// orders_check_transition decides which moves are legal.
var fulfillmentLabels = [...]struct {
	value order.FulfillmentStatus
	label i18n.Key
}{
	{order.FulfillmentPending, i18n.KeyAdminStatusPending},
	{order.FulfillmentPicking, i18n.KeyAdminStatusPicking},
	{order.FulfillmentShipped, i18n.KeyAdminStatusShipped},
	{order.FulfillmentDelivered, i18n.KeyAdminStatusDelivered},
	{order.FulfillmentCompleted, i18n.KeyAdminStatusCompleted},
	{order.FulfillmentCancelled, i18n.KeyAdminStatusCancelled},
}

// FulfillmentLabel answers from the status alone, which is right everywhere but
// 'pending'; the order surfaces use FundedFulfillmentLabel.
func FulfillmentLabel(ctx context.Context, s order.FulfillmentStatus) string {
	for _, status := range fulfillmentLabels {
		if status.value == s {
			return i18n.T(ctx, status.label)
		}
	}
	// Not a panic: a queue opening with one untranslated word beats one that
	// will not load. audit_events is append-only, so a row naming a retired
	// status must still render.
	return string(s)
}

// FundedFulfillmentLabel is a fulfillment state read together with what the order
// owes, which is the only way to tell the two halves of 'pending' apart: an
// order stays pending from the moment the money arrives until a human picks it,
// and one paid entirely from store credit has no payment row at all.
//
// committed means the shop has taken the order on; owed == 0 means nothing is
// due. Either is enough here: a card capture sets the first, and store credit or
// a full discount sets the second.
func FundedFulfillmentLabel(ctx context.Context, status order.FulfillmentStatus, committed bool, owedCents int64) string {
	if readyToPick(status, committed, owedCents) {
		return i18n.T(ctx, i18n.KeyAdminStatusReadyToPick)
	}
	return FulfillmentLabel(ctx, status)
}

func readyToPick(status order.FulfillmentStatus, committed bool, owedCents int64) bool {
	return status == order.FulfillmentPending && (committed || owedCents <= 0)
}

// FundedFulfillmentIntent is the colour group of the word FundedFulfillmentLabel
// gives the same arguments: an order waiting for the customer is neutral, one
// waiting for the shop to pick it needs the staff, picking and shipping are under
// way, and delivered or completed is finished.
func FundedFulfillmentIntent(status order.FulfillmentStatus, committed bool, owedCents int64) components.Intent {
	switch {
	case readyToPick(status, committed, owedCents):
		return components.IntentWarn
	case status == order.FulfillmentPicking, status == order.FulfillmentShipped:
		return components.IntentProgress
	case status == order.FulfillmentDelivered, status == order.FulfillmentCompleted:
		return components.IntentDone
	default:
		return components.IntentNeutral
	}
}
