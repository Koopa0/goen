// Package orders is the back office's order desk: the dashboard, the orders
// queue, one order's page, and what staff do to an order from there: move its
// status, ship its parcels, note it, correct its delivery.
package orders

import (
	"errors"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

var (
	ErrNotFound = errors.New("orders: not found")
	// ErrRefused is a write the database declined; its message is the database's
	// own, because that names the rule.
	ErrRefused = errors.New("orders: refused")

	ErrInvalid = errors.New("orders: invalid input")
	// ErrCarrier is a dispatch naming a carrier that cannot carry this order's
	// parcel: a store order goes with its chain's carrier, a home delivery with a
	// home carrier.
	ErrCarrier  = errors.New("orders: carrier cannot carry this order")
	ErrQuantity = errors.New("orders: quantity out of range")
	// ErrPaidCancel is the status form asked to cancel a paid order. A paid
	// order is cancelled only by refunding it before shipment.
	ErrPaidCancel = errors.New("orders: a paid order is cancelled by refunding it before shipment")
)

// queueTabs is the orders queue's closed set of filters, in the order it shows
// them. Pending is two queues because FundedStatusLabel reads it as two: money
// still owed, and funded and waiting to be picked.
var queueTabs = [...]struct {
	filter admin.QueueFilter
	label  i18n.Key
}{
	{admin.QueueAwaitingPayment, i18n.KeyAdminStatusPending},
	{admin.QueueReady, i18n.KeyAdminStatusReadyToPick},
	{admin.QueuePicking, i18n.KeyAdminStatusPicking},
	{admin.QueueShipped, i18n.KeyAdminStatusShipped},
	{admin.QueueDelivered, i18n.KeyAdminStatusDelivered},
	{admin.QueueCompleted, i18n.KeyAdminStatusCompleted},
	{admin.QueueCancelled, i18n.KeyAdminStatusCancelled},
}

func ParseQueueFilter(s string) admin.QueueFilter {
	for _, tab := range queueTabs {
		if string(tab.filter) == s {
			return tab.filter
		}
	}
	return admin.QueueAll
}

func ParseStatus(s string) pages.FulfillmentStatus {
	status := pages.FulfillmentStatus(s)
	if status.Known() {
		return status
	}
	return ""
}

func NextStatuses(current pages.FulfillmentStatus) []pages.FulfillmentStatus {
	switch current {
	case pages.FulfillmentPending:
		return []pages.FulfillmentStatus{pages.FulfillmentPicking, pages.FulfillmentCancelled}
	case pages.FulfillmentPicking:
		// 'shipped' is absent: [Store.Ship] is the only door, because a dispatch
		// must also settle the stock the order holds.
		return []pages.FulfillmentStatus{pages.FulfillmentCancelled}
	case pages.FulfillmentShipped:
		return []pages.FulfillmentStatus{pages.FulfillmentDelivered, pages.FulfillmentCompleted}
	case pages.FulfillmentDelivered:
		return []pages.FulfillmentStatus{pages.FulfillmentCompleted}
	default:
		return nil
	}
}

// funded reports that money is behind the order: a card capture or its fulfilment
// has committed it, or store credit paid the whole of it while it is still
// pending, which the database does not count as committed until it is picked.
func funded(committed bool, owedCents, creditCents int64) bool {
	return committed || (creditCents > 0 && owedCents <= 0)
}
