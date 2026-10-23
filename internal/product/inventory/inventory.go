// Package inventory names why a variant's stock moved, and reads how long a
// SKU's sellable stock lasts at its rate of sale: the days-cover estimate, its
// policy numbers, and the order the back office lists SKUs at risk in.
package inventory

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
)

// MovementReason is inventory_movements.reason, a closed set that
// inventory_movements_reason_known checks.
type MovementReason string

const (
	ReasonReceipt    MovementReason = "receipt"
	ReasonHold       MovementReason = "hold"
	ReasonSale       MovementReason = "sale"
	ReasonRelease    MovementReason = "release"
	ReasonReturn     MovementReason = "return"
	ReasonAdjustment MovementReason = "adjustment"
)

// reasons is the closed set, in the order the schema lists it, with the
// catalogue key of each one's label.
var reasons = [...]struct {
	reason MovementReason
	label  i18n.Key
}{
	{ReasonReceipt, i18n.KeyAdminMoveReceipt},
	{ReasonHold, i18n.KeyAdminMoveHold},
	{ReasonSale, i18n.KeyAdminMoveSale},
	{ReasonRelease, i18n.KeyAdminMoveRelease},
	{ReasonReturn, i18n.KeyAdminMoveReturn},
	{ReasonAdjustment, i18n.KeyAdminMoveAdjustment},
}

// Label is the back office's word for r. It panics on a value outside the set:
// the schema admits no other, and TestMovementReason_MatchesSchema holds the set
// to it.
func (r MovementReason) Label(ctx context.Context) string {
	for _, known := range reasons {
		if known.reason == r {
			return i18n.T(ctx, known.label)
		}
	}
	panic("inventory: no label for movement reason " + string(r))
}
