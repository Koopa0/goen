// Package inventory names why a variant's stock moved, and reads how long a
// SKU's sellable stock lasts at its rate of sale: the days-cover estimate, its
// policy numbers, and the order the back office lists SKUs at risk in.
package inventory

// MovementReason is inventory_movements.reason.
type MovementReason string

const (
	ReasonReceipt    MovementReason = "receipt"
	ReasonHold       MovementReason = "hold"
	ReasonSale       MovementReason = "sale"
	ReasonRelease    MovementReason = "release"
	ReasonReturn     MovementReason = "return"
	ReasonAdjustment MovementReason = "adjustment"
)
