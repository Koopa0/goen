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

// A pending order store credit alone paid is offered the refund before
// shipment as its cancellation; once packing commits it, or a return exists,
// it takes the refund through a return or none at all.
func TestACreditPaidPendingOrderIsOfferedItsCancellation(t *testing.T) {
	t.Parallel()
	pending, picking := string(order.FulfillmentPending), string(order.FulfillmentPicking)
	for _, tc := range []struct {
		name                string
		row                 db.BeforeShipmentRefundRow
		offered, creditPaid bool
	}{
		{name: "pending, credit paid", row: db.BeforeShipmentRefundRow{FulfillmentStatus: pending, PaidByCredit: true}, offered: true, creditPaid: true},
		{name: "pending, credit paid, committed", row: db.BeforeShipmentRefundRow{FulfillmentStatus: pending, PaidByCredit: true, Committed: true}, offered: true},
		{name: "picking, credit paid, committed", row: db.BeforeShipmentRefundRow{FulfillmentStatus: picking, PaidByCredit: true, Committed: true}, offered: true},
		{name: "picking, credit paid", row: db.BeforeShipmentRefundRow{FulfillmentStatus: picking, PaidByCredit: true}, offered: false},
		{name: "pending, credit paid, has a return", row: db.BeforeShipmentRefundRow{FulfillmentStatus: pending, PaidByCredit: true, HasReturn: true}, offered: false},
		{name: "pending, owes", row: db.BeforeShipmentRefundRow{FulfillmentStatus: pending}, offered: false},
		{name: "cancelled, credit paid", row: db.BeforeShipmentRefundRow{FulfillmentStatus: string(order.FulfillmentCancelled), PaidByCredit: true}, offered: false},
	} {
		offered, open := beforeShipmentRefundState(&tc.row)
		if offered != tc.offered || open {
			t.Errorf("beforeShipmentRefundState(%s) = offered %t open %t, want offered %t open false", tc.name, offered, open, tc.offered)
		}
		if got := creditPaidPending(&tc.row); got != tc.creditPaid {
			t.Errorf("creditPaidPending(%s) = %t, want %t", tc.name, got, tc.creditPaid)
		}
	}
}
