// Package inventory names why a variant's stock moved.
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
