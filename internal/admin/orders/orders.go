// Package orders is the back office's order desk: the orders queue with its
// two work tabs, one order's page, and what staff do to an order from there:
// move its status, ship its parcels, note it, correct its delivery, and file,
// void or correct its uniform invoice; and, from a work tab, starting the
// ticked orders' packing or printing their slips.
package orders

import (
	"errors"

	"github.com/koopa0/goen/internal/i18n"
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
	// ErrAlreadyThere is a move to the status the order already holds, found
	// under the order's lock. A refusal for one order's form, a success for a
	// batch that a second press repeats.
	ErrAlreadyThere = errors.New("orders: the order already has that status")
	// ErrCancelled is a move refused because the order was cancelled.
	ErrCancelled = errors.New("orders: the order is cancelled")
)

// queueTabs is the orders queue's closed set of filters, in the order it shows
// them. Pending is two queues because FundedStatusLabel reads it as two: money
// still owed, and funded and waiting to be picked.
var queueTabs = [...]struct {
	filter QueueFilter
	label  i18n.Key
}{
	{QueueAwaitingPayment, i18n.KeyAdminStatusPending},
	{QueueReady, i18n.KeyAdminStatusReadyToPick},
	{QueuePicking, i18n.KeyAdminStatusPicking},
	{QueueShipped, i18n.KeyAdminStatusShipped},
	{QueueDelivered, i18n.KeyAdminStatusDelivered},
	{QueueCompleted, i18n.KeyAdminStatusCompleted},
	{QueueCancelled, i18n.KeyAdminStatusCancelled},
}

func ParseQueueFilter(s string) QueueFilter {
	for _, tab := range queueTabs {
		if string(tab.filter) == s {
			return tab.filter
		}
	}
	return QueueAll
}

// funded reports that money is behind the order: a card capture or its fulfilment
// has committed it, or store credit paid the whole of it while it is still
// pending, which the database does not count as committed until it is picked.
func funded(committed bool, owedCents, creditCents int64) bool {
	return committed || (creditCents > 0 && owedCents <= 0)
}
