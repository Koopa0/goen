package orders

import (
	"testing"

	"github.com/koopa0/goen/internal/order"
)

func TestOnlyAnOrderStillGoingSomewhereCanHaveItsDeliveryCorrected(t *testing.T) {
	t.Parallel()
	for status, want := range map[order.FulfillmentStatus]bool{
		order.FulfillmentPending:   true,
		order.FulfillmentPicking:   true,
		order.FulfillmentShipped:   false,
		order.FulfillmentDelivered: false,
		order.FulfillmentCompleted: false,
		order.FulfillmentCancelled: false,
	} {
		if got := correctable(status); got != want {
			t.Errorf("correctable(%s) = %t, want %t", status, got, want)
		}
	}
}
