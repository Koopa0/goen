package refunds

import (
	"testing"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/order"
)

// The database admits a refund before shipment from the paid state and from
// picking; the page offers it in both.
func TestTheRefundBeforeShipmentIsOfferedFromPaidAndPicking(t *testing.T) {
	t.Parallel()
	for status, want := range map[order.FulfillmentStatus]bool{
		order.FulfillmentPending:   true,
		order.FulfillmentPicking:   true,
		order.FulfillmentShipped:   false,
		order.FulfillmentCancelled: false,
	} {
		offered, open := beforeShipmentRefundState(&db.BeforeShipmentRefundRow{
			FulfillmentStatus: string(status), Committed: true,
		})
		if offered != want || open {
			t.Errorf("committed %s: offered=%t open=%t, want offered=%t open=false", status, offered, open, want)
		}
	}
	if offered, _ := beforeShipmentRefundState(&db.BeforeShipmentRefundRow{
		FulfillmentStatus: string(order.FulfillmentPending), Committed: false,
	}); offered {
		t.Error("an unpaid order is offered a refund")
	}
}
