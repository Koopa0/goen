package cart

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
)

// Coupon-specific sentinel errors. Each is a distinct thing to tell a customer,
// which is why they are not one ErrCoupon: "expired" and "you already used it"
// send a shopper to different next actions.
var (
	// ErrNoSuchCoupon is a code that names nothing. Deliberately also returned
	// for a coupon that exists but is switched off — a shop should not confirm
	// which of its codes are real to somebody guessing.
	ErrNoSuchCoupon = errors.New("cart: no such coupon")
	// ErrCouponExpired is outside its window.
	ErrCouponExpired = errors.New("cart: coupon expired")
	// ErrCouponMinimum is an order that has not reached the minimum spend.
	ErrCouponMinimum = errors.New("cart: order below the coupon minimum")
	// ErrCouponUsedUp is a coupon at its total or per-customer limit.
	ErrCouponUsedUp = errors.New("cart: coupon already used")
)

// couponCode is coupons_code_format, restated so a malformed code is a message
// rather than a query.
var couponCode = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{1,31}$`)

// Coupon is a coupon as the cart works with it.
type Coupon struct {
	couponValue

	ID          uuid.UUID
	Code        string
	Description string
	Kind        string
	// DiscountCents is what it takes off THIS order, already capped and already
	// bounded by the order total. Computed by Price, not stored.
	DiscountCents int64
	// FreeShipping is set by a free_shipping coupon; the caller zeroes the fee
	// rather than turning it into a discount, so the order still records what
	// delivery would have cost.
	FreeShipping bool
}

// NormaliseCode is what a typed code becomes before it is looked up.
//
// Upper-cased and trimmed, because a code is read off a card by a human. Not
// stripped of hyphens: SUMMER-20 and SUMMER20 are different codes, and quietly
// treating them as one would make two promotions collide.
func NormaliseCode(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// Price works out what a coupon takes off an order.
//
// Everything about the arithmetic is deliberate:
//
//   - The discount is capped at the SUBTOTAL, never the total. A coupon that
//     could eat the shipping fee would let a NT$50 coupon on a NT$50 order
//     produce a negative total, which orders_total_non_negative refuses — and
//     a refusal at the database is a failed checkout rather than a correct one.
//   - A percentage is basis points and integer arithmetic throughout. Within
//     goen's own money ceiling the two agree exactly — 10^10 cents times 10^4
//     basis points is 10^14, well inside float64's exact range — so this is not
//     a bug being avoided today. It is the arithmetic staying obviously correct
//     if the ceiling ever rises, and not needing anybody to re-derive that it
//     is safe. TestMoneyCeilingStaysInsideExactIntegerArithmetic is the alarm.
//   - free_shipping is not a discount. It zeroes the fee, so the order still
//     records what delivery would have cost and reconciliation can see the
//     promotion rather than an unexplained smaller number.
func (c *Coupon) Price(subtotalCents, shippingCents int64) {
	switch c.Kind {
	case "amount":
		c.DiscountCents = min(c.amount, subtotalCents)
	case "percent":
		// Integer basis points: (subtotal * bp) / 10000, truncated. Truncation
		// rounds in the customer's favour by at most one cent, which is the
		// direction to err.
		d := subtotalCents * int64(c.percentBP) / 10000
		if c.capCents > 0 {
			d = min(d, c.capCents)
		}
		c.DiscountCents = min(d, subtotalCents)
	case "free_shipping":
		c.FreeShipping = true
		c.DiscountCents = 0
		_ = shippingCents
	default:
		panic("cart: unknown coupon kind " + c.Kind)
	}
}

// The value fields, unexported because only Price reads them — a caller that
// could see amount and percent could apply the wrong one.
type couponValue struct {
	amount    int64
	percentBP int32
	capCents  int64
	minSpend  int64
}

// FindCoupon looks a code up and prices it against an order.
//
// The window and the limits are NOT checked here: redeem_coupon holds them
// under a lock on the coupon row, and checking them here as well would be
// checking them without one — two concurrent checkouts would each pass. What is
// checked here is what a customer can act on: the code, and the minimum spend.
func (s *Store) FindCoupon(ctx context.Context, code string, subtotalCents, shippingCents int64) (*Coupon, error) {
	code = NormaliseCode(code)
	if !couponCode.MatchString(code) {
		return nil, ErrNoSuchCoupon
	}

	row, err := s.q.CouponByCode(ctx, code)
	if err != nil {
		return nil, ErrNoSuchCoupon
	}
	// A switched-off coupon reads as an unknown one. Saying "this code is
	// disabled" tells somebody guessing which of a shop's codes are real.
	if !row.IsActive {
		return nil, ErrNoSuchCoupon
	}
	// Computed by the query against the database's own clock, not compared
	// here against Go's — see CouponByCode.
	if !row.IsCurrent {
		return nil, ErrCouponExpired
	}
	if subtotalCents < row.MinSubtotalCents {
		return nil, fmt.Errorf("%w: needs %d", ErrCouponMinimum, row.MinSubtotalCents)
	}

	c := &Coupon{
		ID: row.ID, Code: row.Code, Description: row.Description, Kind: row.Kind,
		couponValue: couponValue{
			amount:    row.AmountCents.Int64,
			percentBP: row.PercentBp.Int32,
			capCents:  row.MaxDiscountCents.Int64,
			minSpend:  row.MinSubtotalCents,
		},
	}
	c.Price(subtotalCents, shippingCents)
	return c, nil
}

// redeemCoupon posts the redemption inside the order's transaction.
//
// It runs AFTER the order exists and carries the discount the order was
// actually given, which coupon_redemption_matches_order holds it to.
func redeemCoupon(ctx context.Context, q *db.Queries, c *Coupon, orderID uuid.UUID, userID uuid.NullUUID) error {
	if c == nil {
		return nil
	}
	if _, err := q.RedeemCoupon(ctx, db.RedeemCouponParams{
		CouponID: c.ID, OrderID: orderID, UserID: userID,
		AmountCents: c.DiscountCents,
	}); err != nil {
		// The limits speak here, under the lock redeem_coupon takes. Mapped to
		// a sentinel so a customer sees why rather than a 500.
		msg := err.Error()
		switch {
		case strings.Contains(msg, "coupon_within_total_limit"),
			strings.Contains(msg, "coupon_within_customer_limit"):
			return ErrCouponUsedUp
		case strings.Contains(msg, "coupon_is_current"):
			return ErrCouponExpired
		}
		return fmt.Errorf("redeem coupon %s: %w", c.Code, err)
	}
	return nil
}
