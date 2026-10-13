package shipping

// Destination is shipping_methods.destination_kind and orders' destination.
type Destination string

const (
	// DestinationAddress wants a street address (home delivery).
	DestinationAddress Destination = "address"
	// DestinationPickupPoint wants a convenience-store pickup point.
	DestinationPickupPoint Destination = "pickup_point"
)

// ParseDestination turns a column's value into a Destination, refusing an unknown one rather than
// defaulting to an address nobody can deliver to.
func ParseDestination(kind string) (Destination, bool) {
	switch Destination(kind) {
	case DestinationAddress:
		return DestinationAddress, true
	case DestinationPickupPoint:
		return DestinationPickupPoint, true
	}
	return "", false
}
