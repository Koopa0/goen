// Package orderstatus is how the back office names an order's fulfilment state:
// one label table, read by every screen that shows an order.
package orderstatus

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// statuses is the fulfilment lifecycle, in the order the queue shows it.
// orders_check_transition decides which moves are legal.
var statuses = [...]struct {
	value pages.FulfillmentStatus
	label i18n.Key
}{
	{pages.FulfillmentPending, i18n.KeyAdminStatusPending},
	{pages.FulfillmentPicking, i18n.KeyAdminStatusPicking},
	{pages.FulfillmentShipped, i18n.KeyAdminStatusShipped},
	{pages.FulfillmentDelivered, i18n.KeyAdminStatusDelivered},
	{pages.FulfillmentCompleted, i18n.KeyAdminStatusCompleted},
	{pages.FulfillmentCancelled, i18n.KeyAdminStatusCancelled},
}

// Label answers from the status alone, which is right everywhere but
// 'pending'; the order surfaces use FundedLabel.
func Label(ctx context.Context, s pages.FulfillmentStatus) string {
	for _, status := range statuses {
		if status.value == s {
			return i18n.T(ctx, status.label)
		}
	}
	// Not a panic: a queue opening with one untranslated word beats one that
	// will not load. audit_events is append-only, so a row naming a retired
	// status must still render.
	return string(s)
}

// FundedLabel is a fulfilment state read together with what the order
// owes, which is the only way to tell the two halves of 'pending' apart: an
// order stays pending from the moment the money arrives until a human picks it,
// and one paid entirely from store credit has no payment row at all.
//
// committed means the shop has taken the order on; owed == 0 means nothing is
// due. Either is enough here: a card capture sets the first, and store credit or
// a full discount sets the second.
func FundedLabel(ctx context.Context, status pages.FulfillmentStatus, committed bool, owedCents int64) string {
	if status == pages.FulfillmentPending && (committed || owedCents <= 0) {
		return i18n.T(ctx, i18n.KeyAdminStatusReadyToPick)
	}
	return Label(ctx, status)
}
