package order

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
)

// fulfillmentLabels is the fulfillment lifecycle, in the order the queue shows it.
// orders_check_transition decides which moves are legal.
var fulfillmentLabels = [...]struct {
	value FulfillmentStatus
	label i18n.Key
}{
	{FulfillmentPending, i18n.KeyAdminStatusPending},
	{FulfillmentPicking, i18n.KeyAdminStatusPicking},
	{FulfillmentShipped, i18n.KeyAdminStatusShipped},
	{FulfillmentDelivered, i18n.KeyAdminStatusDelivered},
	{FulfillmentCompleted, i18n.KeyAdminStatusCompleted},
	{FulfillmentCancelled, i18n.KeyAdminStatusCancelled},
}

// Label is the back office's word for s. It answers from the status alone,
// which is right everywhere but 'pending'; the order surfaces use FundedLabel.
// The storefront tells a buyer about their order in words of its own.
func (s FulfillmentStatus) Label(ctx context.Context) string {
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

// FundedLabel is s read together with what the order owes, which is the only
// way to tell the two halves of 'pending' apart: an order stays pending from
// the moment the money arrives until a human picks it, and one paid entirely
// from store credit has no payment row at all.
//
// committed means the shop has taken the order on; owed == 0 means nothing is
// due. Either is enough here: a card capture sets the first, and store credit or
// a full discount sets the second.
func (s FulfillmentStatus) FundedLabel(ctx context.Context, committed bool, owedCents int64) string {
	if s.ReadyToPick(committed, owedCents) {
		return i18n.T(ctx, i18n.KeyAdminStatusReadyToPick)
	}
	return s.Label(ctx)
}

// ReadyToPick reports whether an order in status s waits for the shop rather
// than for the customer: it is pending and either committed or owes nothing.
func (s FulfillmentStatus) ReadyToPick(committed bool, owedCents int64) bool {
	return s == FulfillmentPending && (committed || owedCents <= 0)
}
