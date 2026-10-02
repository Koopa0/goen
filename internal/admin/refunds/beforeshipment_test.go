package refunds

import (
	"testing"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
)

// The database admits a refund before shipment from the paid state and from
// picking; the page offers it in both.
func TestTheRefundBeforeShipmentIsOfferedFromPaidAndPicking(t *testing.T) {
	t.Parallel()
	for status, want := range map[pages.FulfillmentStatus]bool{
		pages.FulfillmentPending:   true,
		pages.FulfillmentPicking:   true,
		pages.FulfillmentShipped:   false,
		pages.FulfillmentCancelled: false,
	} {
		offered, open := beforeShipmentRefundState(&db.BeforeShipmentRefundRow{
			FulfillmentStatus: string(status), Committed: true,
		})
		if offered != want || open {
			t.Errorf("committed %s: offered=%t open=%t, want offered=%t open=false", status, offered, open, want)
		}
	}
	if offered, _ := beforeShipmentRefundState(&db.BeforeShipmentRefundRow{
		FulfillmentStatus: string(pages.FulfillmentPending), Committed: false,
	}); offered {
		t.Error("an unpaid order is offered a refund")
	}
}
