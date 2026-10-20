// Package coupon is what a coupon takes off an order and the shape of its
// code; it holds the kinds, Coupon, Apply and its refusals, Normalise and
// ValidCode; it does not hold the lookup and redemption that run inside
// checkout's transaction (cart), a campaign's price strike (campaign) or
// store credit (credit).
//
// Checkout prices an order by Apply and the back office stores the codes
// checkout looks up, so both read the rule and the code shape from here.
package coupon

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Kind is coupons.kind.
type Kind string

const (
	Amount       Kind = "amount"
	Percent      Kind = "percent"
	FreeShipping Kind = "free_shipping"
)

// Parse turns a column's or form's value into a Kind, refusing an unknown one.
func Parse(s string) (Kind, bool) {
	switch k := Kind(s); k {
	case Amount, Percent, FreeShipping:
		return k, true
	}
	return "", false
}

var (
	// ErrNotFound covers a code that names nothing and one that is switched
	// off, so a shop does not confirm which codes are real to somebody
	// guessing.
	ErrNotFound     = errors.New("coupon: no such coupon")
	ErrExpired      = errors.New("coupon: expired")
	ErrBelowMinimum = errors.New("coupon: order below the coupon minimum")
	ErrUsedUp       = errors.New("coupon: already used")
	// ErrNotNeeded is a free-delivery code on an order whose base rate is
	// already nothing: it would take nothing off and still spend a redemption.
	ErrNotNeeded = errors.New("coupon: order already ships free")
)

// Coupon is one coupons row's terms. The value columns are as the row holds
// them: only the kind's own column is set.
type Coupon struct {
	ID          uuid.UUID
	Code        string
	Description string
	Kind        Kind

	AmountCents  int64
	PercentBP    int32
	CapCents     int64
	MinimumCents int64
}

const centsPerYuan = 100

// Apply caps a discount at the subtotal, never the total, which
// orders_total_non_negative refuses. shipsFree says the order's base delivery
// rate is already nothing, which is all a free-delivery code would remove.
// The schema restates it as coupon_discount, whose comment in the frozen
// migrations/001 still names its former home, cart.Coupon.Apply;
// TestCouponPricing holds the two equal.
func (c Coupon) Apply(subtotalCents int64, shipsFree bool) (discountCents int64, freeShipping bool, err error) {
	if subtotalCents < c.MinimumCents {
		return 0, false, fmt.Errorf("%w: needs %d", ErrBelowMinimum, c.MinimumCents)
	}

	switch c.Kind {
	case Amount:
		return min(c.AmountCents, subtotalCents), false, nil
	case Percent:
		// Split quotient and remainder before multiplying: subtotalCents may
		// fit in int64 while subtotalCents*percentBP does not.
		basisPoints := int64(c.PercentBP)
		discountCents = subtotalCents/10000*basisPoints +
			subtotalCents%10000*basisPoints/10000
		// The discount is a whole NT$, so the quoted total, the card charge and
		// the invoice agree; refunds of a partial return and store-credit
		// splits still carry cents. It rounds UP, in the customer's favour. The
		// cap and the subtotal bind after rounding, each floored to a whole
		// NT$, and the limit is checked first so the round-up cannot overflow
		// near int64's end.
		limit := subtotalCents
		if c.CapCents > 0 {
			limit = min(limit, c.CapCents)
		}
		limit -= limit % centsPerYuan
		if discountCents >= limit {
			return limit, false, nil
		}
		if remainder := discountCents % centsPerYuan; remainder != 0 {
			discountCents += centsPerYuan - remainder
		}
		return discountCents, false, nil
	case FreeShipping:
		if shipsFree {
			return 0, false, ErrNotNeeded
		}
		return 0, true, nil
	default:
		return 0, false, fmt.Errorf("%w: coupon %s has unknown kind %q", ErrNotFound, c.Code, c.Kind)
	}
}
