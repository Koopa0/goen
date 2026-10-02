package pages

// FulfillmentStatus is closed by orders_fulfillment_status_known. Declared here
// because admin, cart and account all build these view models; a type any of
// them owned could not be named below without a cycle.
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
