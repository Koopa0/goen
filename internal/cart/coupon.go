package cart

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/db"
)

var (
	// ErrNoSuchCoupon is a code that names nothing, and also one that is switched
	// off, so a shop does not confirm which codes are real to somebody guessing.
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

// Coupon is a stable coupon definition as the cart works with it.
type Coupon struct {
	id          uuid.UUID
	code        string
	description string
	kind        string

	amountCents  int64
	percentBP    int32
	capCents     int64
	minimumCents int64
}

// NormaliseCode upper-cases and trims a typed code, leaving hyphens alone.
func NormaliseCode(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// Apply works out what this definition does to one order without retaining the
// result. A discount is capped at the subtotal and never the total, which
// orders_total_non_negative refuses.
func (c Coupon) Apply(subtotalCents int64) (discountCents int64, freeShipping bool, err error) {
	if subtotalCents < c.minimumCents {
		return 0, false, fmt.Errorf("%w: needs %d", ErrCouponMinimum, c.minimumCents)
	}

	switch c.kind {
	case "amount":
		return min(c.amountCents, subtotalCents), false, nil
	case "percent":
		// Integer basis points throughout; division rounds the discount down by
		// less than one cent. Split quotient and remainder before multiplying:
		// subtotalCents itself may legitimately fit in int64 while
		// subtotalCents*percentBP does not.
		basisPoints := int64(c.percentBP)
		discountCents = subtotalCents/10000*basisPoints +
			subtotalCents%10000*basisPoints/10000
		if c.capCents > 0 {
			discountCents = min(discountCents, c.capCents)
		}
		return min(discountCents, subtotalCents), false, nil
	case "free_shipping":
		return 0, true, nil
	default:
		// Coupon fields are private and FindCoupon reads a schema-constrained
		// closed set. Reaching this arm is binary/schema skew or an internal
		// construction bug, not input the customer can repair with another code.
		panic(fmt.Sprintf("cart: coupon %s has unknown kind %q", c.code, c.kind))
	}
}

// FindCoupon looks a code up. Order-specific eligibility and value belong to
// Apply; redemption limits are counted by redeem_coupon under a coupon-row lock.
func (s *Store) FindCoupon(ctx context.Context, code string) (*Coupon, error) {
	return findCoupon(ctx, s.q, code)
}

// findCoupon uses the caller's query binding. Checkout first calls the narrow
// lock_coupon_for_checkout door on that same binding, so this ordinary read and
// the later quote comparison/redemption stay in the row-locking transaction.
func findCoupon(ctx context.Context, q *db.Queries, code string) (*Coupon, error) {
	code = NormaliseCode(code)
	if !couponCode.MatchString(code) {
		return nil, ErrNoSuchCoupon
	}

	row, err := q.CouponByCode(ctx, code)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNoSuchCoupon
		}
		return nil, fmt.Errorf("find coupon %q: %w", code, err)
	}
	if !row.IsActive {
		return nil, ErrNoSuchCoupon
	}
	// Decided by the query, against the database's own clock and never Go's.
	if !row.IsCurrent {
		return nil, ErrCouponExpired
	}

	return &Coupon{
		id:           row.ID,
		code:         row.Code,
		description:  row.Description,
		kind:         row.Kind,
		amountCents:  row.AmountCents.Int64,
		percentBP:    row.PercentBp.Int32,
		capCents:     row.MaxDiscountCents.Int64,
		minimumCents: row.MinSubtotalCents,
	}, nil
}

// redeemCoupon posts the redemption inside the order's transaction, carrying the
// discount given — coupon_redemption_matches_order holds the two to each other.
func redeemCoupon(
	ctx context.Context,
	q *db.Queries,
	c *Coupon,
	discountCents int64,
	orderID uuid.UUID,
	userID uuid.NullUUID,
) error {
	if c == nil {
		return nil
	}
	if _, err := q.RedeemCoupon(ctx, db.RedeemCouponParams{
		CouponID: c.id, OrderID: orderID, UserID: userID,
		AmountCents: discountCents,
	}); err != nil {
		// Bound to the constraint NAME, never to the message: pgconn renders a
		// PgError as severity + message + SQLSTATE, and the name is not in it.
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
			switch pgErr.ConstraintName {
			case "coupon_within_total_limit", "coupon_within_customer_limit":
				return ErrCouponUsedUp
			case "coupon_is_current":
				return ErrCouponExpired
			}
		}
		return fmt.Errorf("redeem coupon %s: %w", c.code, err)
	}
	return nil
}
