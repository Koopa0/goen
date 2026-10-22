package orderstatus

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/widget"
)

func TestIntentGroupsEachOrderState(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		status    order.FulfillmentStatus
		committed bool
		owed      int64
		want      widget.Intent
	}{
		{"waiting for the customer", order.FulfillmentPending, false, 100, widget.IntentNeutral},
		{"paid, waiting for the shop", order.FulfillmentPending, true, 0, widget.IntentWarn},
		{"paid by credit alone", order.FulfillmentPending, false, 0, widget.IntentWarn},
		{"picking", order.FulfillmentPicking, true, 0, widget.IntentProgress},
		{"shipped", order.FulfillmentShipped, true, 0, widget.IntentProgress},
		{"delivered", order.FulfillmentDelivered, true, 0, widget.IntentDone},
		{"completed", order.FulfillmentCompleted, true, 0, widget.IntentDone},
		{"cancelled", order.FulfillmentCancelled, false, 0, widget.IntentNeutral},
		{"a retired state", order.FulfillmentStatus("retired"), false, 0, widget.IntentNeutral},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := intent(tt.status, tt.committed, tt.owed); got != tt.want {
				t.Errorf("intent(%q, %t, %d) = %q, want %q", tt.status, tt.committed, tt.owed, got, tt.want)
			}
		})
	}
}

func TestIntentGivesEveryActiveStateAGroup(t *testing.T) {
	t.Parallel()
	for _, s := range order.FulfillmentStatuses {
		if s == order.FulfillmentCancelled || s == order.FulfillmentPending {
			continue
		}
		if intent(s, true, 0) == widget.IntentNeutral {
			t.Errorf("%q falls to neutral, the group of states nobody acts on", s)
		}
	}
}

// TestBadgeSaysAReturnedOrderWasRefunded: an order returned in full says it was
// refunded, in the neutral group, whatever its delivery says; any other order
// keeps its delivery word and that word's colour.
func TestBadgeSaysAReturnedOrderWasRefunded(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	text, got := Badge(ctx, order.FulfillmentDelivered, true, 0, true)
	if want := i18n.T(ctx, i18n.KeyStatusRefunded); text != want || got != widget.IntentNeutral {
		t.Errorf("Badge(delivered, returned) = %q, %q; want %q, %q", text, got, want, widget.IntentNeutral)
	}
	text, got = Badge(ctx, order.FulfillmentDelivered, true, 0, false)
	if want := order.FulfillmentDelivered.FundedLabel(ctx, true, 0); text != want || got != widget.IntentDone {
		t.Errorf("Badge(delivered, not returned) = %q, %q; want %q, %q", text, got, want, widget.IntentDone)
	}
}
