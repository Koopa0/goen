// Package order is the vocabulary of an order: its fulfilment states, event kinds and number.
package order

// FulfillmentStatus is orders.fulfillment_status, closed by orders_fulfillment_status_known.
type FulfillmentStatus string

const (
	FulfillmentPending   FulfillmentStatus = "pending"
	FulfillmentPicking   FulfillmentStatus = "picking"
	FulfillmentShipped   FulfillmentStatus = "shipped"
	FulfillmentDelivered FulfillmentStatus = "delivered"
	FulfillmentCompleted FulfillmentStatus = "completed"
	FulfillmentCancelled FulfillmentStatus = "cancelled"
)

var FulfillmentStatuses = [...]FulfillmentStatus{
	FulfillmentPending,
	FulfillmentPicking,
	FulfillmentShipped,
	FulfillmentDelivered,
	FulfillmentCompleted,
	FulfillmentCancelled,
}

// Known is false for a retired value from append-only history, which must still render.
func (s FulfillmentStatus) Known() bool {
	for _, candidate := range FulfillmentStatuses {
		if s == candidate {
			return true
		}
	}
	return false
}

// Next is the states orders_check_transition lets an order move to from s.
func (s FulfillmentStatus) Next() []FulfillmentStatus {
	switch s {
	case FulfillmentPending:
		return []FulfillmentStatus{FulfillmentPicking, FulfillmentCancelled}
	case FulfillmentPicking:
		return []FulfillmentStatus{FulfillmentShipped, FulfillmentCancelled}
	case FulfillmentShipped:
		return []FulfillmentStatus{FulfillmentDelivered, FulfillmentCompleted}
	case FulfillmentDelivered:
		return []FulfillmentStatus{FulfillmentCompleted}
	default:
		return nil
	}
}

// EventKind is order_events.kind, closed by order_events_kind_known.
type EventKind string

const (
	EventPlaced    EventKind = "placed"
	EventPaid      EventKind = "paid"
	EventPicking   EventKind = "picking"
	EventShipped   EventKind = "shipped"
	EventInTransit EventKind = "in_transit"
	EventDelivered EventKind = "delivered"
	EventCompleted EventKind = "completed"
	EventCancelled EventKind = "cancelled"
	EventRefunded  EventKind = "refunded"
)

// ValidNumber reports whether s has the shape next_order_number() produces:
// GO-YYMMDD-NNNNNN, matching the schema's orders_number_format CHECK. Callers
// concatenate it into a redirect, so the whole shape is validated.
func ValidNumber(s string) bool {
	if len(s) != 16 || s[:3] != "GO-" || s[9] != '-' {
		return false
	}
	for i, r := range s {
		if i == 0 || i == 1 || i == 2 || i == 9 {
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
