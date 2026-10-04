package admin

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
)

// Every status has a catalogue label, in both languages' catalogue.
func TestEveryStatusHasACatalogueLabel(t *testing.T) {
	t.Parallel()
	for _, status := range fulfillmentLabels {
		if got := FulfillmentLabel(t.Context(), status.value); got == "" || got == string(status.value) {
			t.Errorf("FulfillmentLabel(%q) = %q, want a catalogue label", status.value, got)
		}
	}
}

// TestTheLabelTableIsTheFulfillmentClosedSet holds the two halves together:
// order.FulfillmentStatuses is what cart, account and the queue carry, and
// fulfillmentLabels is what the labels cover, so a state added to one and forgotten in
// the other is a label that never appears.
func TestTheLabelTableIsTheFulfillmentClosedSet(t *testing.T) {
	t.Parallel()
	if len(fulfillmentLabels) != len(order.FulfillmentStatuses) {
		t.Fatalf("admin catalogue has %d states, order.FulfillmentStatuses has %d",
			len(fulfillmentLabels), len(order.FulfillmentStatuses))
	}
	for i, want := range order.FulfillmentStatuses {
		if fulfillmentLabels[i].value != want {
			t.Errorf("fulfillmentLabels[%d] = %q, want %q — the queue order drifted from the closed set",
				i, fulfillmentLabels[i].value, want)
		}
	}
}

// TestLabelRendersARetiredStatus holds the reason FulfillmentLabel is not a
// panic: audit_events is append-only, so a row naming a state the shop no
// longer occupies must still open.
func TestLabelRendersARetiredStatus(t *testing.T) {
	t.Parallel()
	const retired order.FulfillmentStatus = "packing"
	if got := FulfillmentLabel(t.Context(), retired); got != string(retired) {
		t.Errorf("FulfillmentLabel(%q) = %q, want the raw value so the queue still loads",
			retired, got)
	}
}

// TestAFundedOrderIsNotBadgedUnpaid holds the two halves of 'pending' apart: an
// order stays pending from the moment money arrives until a human picks it, and
// one paid entirely from store credit has no payment row at all. Committed alone
// means the shop has taken it on, owed == 0 alone means nothing is due, and
// either is enough to say it is not awaiting payment.
func TestAFundedOrderIsNotBadgedUnpaid(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	unpaid := i18n.T(ctx, i18n.KeyAdminStatusPending)
	ready := i18n.T(ctx, i18n.KeyAdminStatusReadyToPick)

	for _, tt := range []struct {
		name      string
		status    order.FulfillmentStatus
		committed bool
		owed      int64
		want      string
	}{
		{name: "nobody has paid", status: "pending", owed: 65000, want: unpaid},
		{name: "the card cleared", status: "pending", committed: true, owed: 65000, want: ready},
		{name: "store credit covered it", status: "pending", owed: 0, want: ready},
		// Every other status answers from itself: only pending is two states
		// wearing one name.
		{name: "picking", status: "picking", committed: true, want: i18n.T(ctx, i18n.KeyAdminStatusPicking)},
		{name: "cancelled and unpaid", status: "cancelled", owed: 65000, want: i18n.T(ctx, i18n.KeyAdminStatusCancelled)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := FundedFulfillmentLabel(ctx, tt.status, tt.committed, tt.owed); got != tt.want {
				t.Errorf("FundedFulfillmentLabel(%q, committed=%v, owed=%d) = %q, want %q",
					tt.status, tt.committed, tt.owed, got, tt.want)
			}
		})
	}
}
