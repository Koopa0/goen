package widget

import (
	"testing"

	"github.com/koopa0/goen/internal/order"
)

func TestFulfillmentIntentGroupsEachOrderState(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		status    order.FulfillmentStatus
		committed bool
		owed      int64
		want      Intent
	}{
		{"waiting for the customer", order.FulfillmentPending, false, 100, IntentNeutral},
		{"paid, waiting for the shop", order.FulfillmentPending, true, 0, IntentWarn},
		{"paid by credit alone", order.FulfillmentPending, false, 0, IntentWarn},
		{"picking", order.FulfillmentPicking, true, 0, IntentProgress},
		{"shipped", order.FulfillmentShipped, true, 0, IntentProgress},
		{"delivered", order.FulfillmentDelivered, true, 0, IntentDone},
		{"completed", order.FulfillmentCompleted, true, 0, IntentDone},
		{"cancelled", order.FulfillmentCancelled, false, 0, IntentNeutral},
		{"a retired state", order.FulfillmentStatus("retired"), false, 0, IntentNeutral},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := FundedFulfillmentIntent(tt.status, tt.committed, tt.owed); got != tt.want {
				t.Errorf("FundedFulfillmentIntent(%q, %t, %d) = %q, want %q", tt.status, tt.committed, tt.owed, got, tt.want)
			}
		})
	}
}

func TestEveryFulfillmentStateHasAGroup(t *testing.T) {
	t.Parallel()
	for _, s := range order.FulfillmentStatuses {
		if s == order.FulfillmentCancelled || s == order.FulfillmentPending {
			continue
		}
		if FundedFulfillmentIntent(s, true, 0) == IntentNeutral {
			t.Errorf("%q falls to neutral, the group of states nobody acts on", s)
		}
	}
}
