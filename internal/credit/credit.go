// Package credit is store credit, a customer's balance at the shop; it holds
// the closed set of posting kinds (store_credit_entries.kind) and how each
// reads; it does not hold points (loyalty), money back to a card (refund) or a
// code discount (coupon).
//
// The store-credit desk and the order page both name a posting by its kind,
// and a desk may not import another desk, so the vocabulary lives here, where
// either can import it.
package credit

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
)

// Kind is store_credit_entries.kind: what a posting is, derived by the
// database from its idempotency key and closed by
// store_credit_entries_kind_known. It is not the staff-written reason.
type Kind string

const (
	OrderSpend       Kind = "order_spend"
	OrderReversal    Kind = "order_cancel_reversal"
	ReturnPayout     Kind = "return_payout"
	PointsRedemption Kind = "points_redemption"
	StaffGrant       Kind = "staff_grant"
)

// Reason is how a posting reads to the person looking at it: the system kinds
// in the reader's language, and a staff grant as the text staff typed. It reads
// kind and never matches reason, so a grant worded "points" stays a grant.
func Reason(ctx context.Context, kind Kind, reason string) string {
	switch kind {
	case OrderSpend:
		return i18n.T(ctx, i18n.KeyAdminCreditReasonOrderSpend)
	case OrderReversal:
		return i18n.T(ctx, i18n.KeyAdminCreditReasonOrderReversed)
	case ReturnPayout:
		return i18n.T(ctx, i18n.KeyAdminCreditReasonReturnPayout)
	case PointsRedemption:
		return i18n.T(ctx, i18n.KeyAdminCreditReasonPoints)
	case StaffGrant:
		return reason
	default:
		panic("credit: no label for store credit kind " + string(kind))
	}
}
