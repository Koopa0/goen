// Package destination owns the closed set of places a shipping method delivers
// to, so a view and a store can name one without importing the checkout.
package destination

// Kind is shipping_methods.destination_kind and orders' destination.
type Kind string

const (
	// Address wants a street address (home delivery).
	Address Kind = "address"
	// PickupPoint wants a convenience-store pickup point.
	PickupPoint Kind = "pickup_point"
)

// For turns a column's value into a Kind, refusing an unknown one rather than
// defaulting to an address nobody can deliver to.
func For(kind string) (Kind, bool) {
	switch Kind(kind) {
	case Address:
		return Address, true
	case PickupPoint:
		return PickupPoint, true
	}
	return "", false
}
