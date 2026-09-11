package pages

// FulfillmentStatus is an order's place in the fulfilment lifecycle, closed by
// orders_fulfillment_status_known. Declared here rather than in internal/admin
// because admin, cart and account build these view models, so a type any of
// them owned could not be named below without closing a cycle.
type FulfillmentStatus string

// The six states an order can occupy.
const (
	FulfillmentPending   FulfillmentStatus = "pending"
	FulfillmentPicking   FulfillmentStatus = "picking"
	FulfillmentShipped   FulfillmentStatus = "shipped"
	FulfillmentDelivered FulfillmentStatus = "delivered"
	FulfillmentCompleted FulfillmentStatus = "completed"
	FulfillmentCancelled FulfillmentStatus = "cancelled"
)

// FulfillmentStatuses is that closed set, in the order the queue shows it.
var FulfillmentStatuses = [...]FulfillmentStatus{
	FulfillmentPending,
	FulfillmentPicking,
	FulfillmentShipped,
	FulfillmentDelivered,
	FulfillmentCompleted,
	FulfillmentCancelled,
}

// Known reports whether s is one of the fulfilment states this shop can occupy.
// A retired value from append-only history is not Known and must still render.
func (s FulfillmentStatus) Known() bool {
	for _, candidate := range FulfillmentStatuses {
		if s == candidate {
			return true
		}
	}
	return false
}
