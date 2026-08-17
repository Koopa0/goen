package cart

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/db"
)

var (
	// ErrNoSuchCoupon is a code that names nothing. Also returned for a coupon
	// that exists but is switched off, so a shop does not confirm which of its
	// codes are real to somebody guessing.
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
	// DiscountCents is what it takes off this order, computed by Price.
	DiscountCents int64
	// FreeShipping is set by a free_shipping coupon; the caller zeroes the fee
	// rather than turning it into a discount, so the order still records what
	// delivery would have cost.
	FreeShipping bool
}

// NormaliseCode upper-cases and trims a typed code. Hyphens are not stripped:
// SUMMER-20 and SUMMER20 are different codes, and folding them would make two
// promotions collide.
func NormaliseCode(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// Price works out what a coupon takes off an order.
//
// The discount is capped at the subtotal and never the total: one that ate the
// shipping fee would drive the total negative, which
// orders_total_non_negative refuses in the middle of a checkout.
func (c *Coupon) Price(subtotalCents, shippingCents int64) {
	switch c.Kind {
	case "amount":
		c.DiscountCents = min(c.amount, subtotalCents)
	case "percent":
		// Integer basis points throughout; the truncation rounds in the
		// customer's favour by at most one cent.
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

type couponValue struct {
	amount    int64
	percentBP int32
	capCents  int64
	minSpend  int64
}

// FindCoupon looks a code up and prices it against an order.
//
// The limits are NOT checked here: redeem_coupon counts them under a lock on the
// coupon row, and counting them here as well would be counting them without one.
func (s *Store) FindCoupon(ctx context.Context, code string, subtotalCents, shippingCents int64) (*Coupon, error) {
	code = NormaliseCode(code)
	if !couponCode.MatchString(code) {
		return nil, ErrNoSuchCoupon
	}

	row, err := s.q.CouponByCode(ctx, code)
	if err != nil {
		return nil, ErrNoSuchCoupon
	}
	if !row.IsActive {
		return nil, ErrNoSuchCoupon
	}
	// Computed by the query against the database's own clock, never against Go's.
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

// redeemCoupon posts the redemption inside the order's transaction, carrying
// the discount the order was actually given —
// coupon_redemption_matches_order holds the two to each other.
func redeemCoupon(ctx context.Context, q *db.Queries, c *Coupon, orderID uuid.UUID, userID uuid.NullUUID) error {
	if c == nil {
		return nil
	}
	if _, err := q.RedeemCoupon(ctx, db.RedeemCouponParams{
		CouponID: c.ID, OrderID: orderID, UserID: userID,
		AmountCents: c.DiscountCents,
	}); err != nil {
		// Bound to the constraint NAME, never to the message: pgconn renders a
		// PgError as severity + message + SQLSTATE, and the name RAISE sets is
		// not in that string at all.
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
			switch pgErr.ConstraintName {
			case "coupon_within_total_limit", "coupon_within_customer_limit":
				return ErrCouponUsedUp
			case "coupon_is_current":
				return ErrCouponExpired
			}
		}
		return fmt.Errorf("redeem coupon %s: %w", c.Code, err)
	}
	return nil
}
