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

	"github.com/koopa0/goen/internal/coupon"
	"github.com/koopa0/goen/internal/db"
)

var (
	// ErrNoSuchCoupon covers a code that names nothing and one that is switched
	// off, so a shop does not confirm which codes are real to somebody
	// guessing.
	ErrNoSuchCoupon  = errors.New("cart: no such coupon")
	ErrCouponExpired = errors.New("cart: coupon expired")
	ErrCouponMinimum = errors.New("cart: order below the coupon minimum")
	ErrCouponUsedUp  = errors.New("cart: coupon already used")
)

// couponCode restates coupons_code_format so a malformed code is a message
// rather than a query.
var couponCode = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{1,31}$`)

type Coupon struct {
	id          uuid.UUID
	code        string
	description string
	kind        coupon.Kind

	amountCents  int64
	percentBP    int32
	capCents     int64
	minimumCents int64
}

const centsPerYuan = 100

func NormaliseCode(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// Apply caps a discount at the subtotal, never the total, which
// orders_total_non_negative refuses.
func (c Coupon) Apply(subtotalCents int64) (discountCents int64, freeShipping bool, err error) {
	if subtotalCents < c.minimumCents {
		return 0, false, fmt.Errorf("%w: needs %d", ErrCouponMinimum, c.minimumCents)
	}

	switch c.kind {
	case coupon.Amount:
		return min(c.amountCents, subtotalCents), false, nil
	case coupon.Percent:
		// Split quotient and remainder before multiplying: subtotalCents may
		// fit in int64 while subtotalCents*percentBP does not.
		basisPoints := int64(c.percentBP)
		discountCents = subtotalCents/10000*basisPoints +
			subtotalCents%10000*basisPoints/10000
		// The discount is a whole NT$, so the quoted total, the card charge and
		// the invoice agree; refunds of a partial return and store-credit
		// splits still carry cents. It rounds UP, in the customer's favour. The
		// cap and the subtotal bind after rounding, each floored to a whole
		// NT$, and the limit is checked first so the round-up cannot overflow
		// near int64's end.
		limit := subtotalCents
		if c.capCents > 0 {
			limit = min(limit, c.capCents)
		}
		limit -= limit % centsPerYuan
		if discountCents >= limit {
			return limit, false, nil
		}
		if remainder := discountCents % centsPerYuan; remainder != 0 {
			discountCents += centsPerYuan - remainder
		}
		return discountCents, false, nil
	case coupon.FreeShipping:
		return 0, true, nil
	default:
		return 0, false, fmt.Errorf("%w: coupon %s has unknown kind %q", ErrNoSuchCoupon, c.code, c.kind)
	}
}

// CouponByCode leaves eligibility and value to Apply; redemption limits are
// counted by redeem_coupon under a coupon-row lock.
func (s *Store) CouponByCode(ctx context.Context, code string) (*Coupon, error) {
	return couponByCode(ctx, s.q, code)
}

// couponByCode runs on the caller's binding: checkout first calls
// lock_coupon_for_checkout on it, so this read and the later redemption stay in
// the row-locking transaction.
func couponByCode(ctx context.Context, q *db.Queries, code string) (*Coupon, error) {
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
		kind:         coupon.Kind(row.Kind),
		amountCents:  row.AmountCents.Int64,
		percentBP:    row.PercentBp.Int32,
		capCents:     row.MaxDiscountCents.Int64,
		minimumCents: row.MinSubtotalCents,
	}, nil
}

// redeemCoupon carries the discount given: coupon_redemption_matches_order
// holds the two to each other.
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
		// Bound to the constraint NAME: pgconn renders a PgError as severity +
		// message + SQLSTATE, and the name is not in it.
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
