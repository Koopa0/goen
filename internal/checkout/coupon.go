package checkout

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/coupon"
	"github.com/koopa0/goen/internal/db"
)

// CouponByCode leaves eligibility and value to Apply; redemption limits are
// counted by redeem_coupon under a coupon-row lock.
func (s *Store) CouponByCode(ctx context.Context, code string) (*coupon.Coupon, error) {
	return couponByCode(ctx, s.q, code)
}

// couponByCode runs on the caller's binding: checkout first calls
// lock_coupon_for_checkout on it, so this read and the later redemption stay in
// the row-locking transaction.
func couponByCode(ctx context.Context, q *db.Queries, code string) (*coupon.Coupon, error) {
	code = coupon.Normalise(code)
	if !coupon.ValidCode(code) {
		return nil, coupon.ErrNotFound
	}

	row, err := q.CouponByCode(ctx, code)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, coupon.ErrNotFound
		}
		return nil, fmt.Errorf("find coupon %q: %w", code, err)
	}
	if !row.IsActive {
		return nil, coupon.ErrNotFound
	}
	// Decided by the query, against the database's own clock and never Go's.
	if !row.IsCurrent {
		return nil, coupon.ErrExpired
	}

	kind, ok := coupon.Parse(row.Kind)
	if !ok {
		return nil, fmt.Errorf("%w: coupon %s has unknown kind %q", coupon.ErrNotFound, row.Code, row.Kind)
	}

	return &coupon.Coupon{
		ID:           row.ID,
		Code:         row.Code,
		Description:  row.Description,
		Kind:         kind,
		AmountCents:  row.AmountCents.Int64,
		PercentBP:    row.PercentBp.Int32,
		CapCents:     row.MaxDiscountCents.Int64,
		MinimumCents: row.MinSubtotalCents,
	}, nil
}

// redeemCoupon carries the discount given: coupon_redemption_matches_order
// holds the two to each other.
func redeemCoupon(
	ctx context.Context,
	q *db.Queries,
	c *coupon.Coupon,
	discountCents int64,
	orderID uuid.UUID,
	userID uuid.NullUUID,
) error {
	if c == nil {
		return nil
	}
	if _, err := q.RedeemCoupon(ctx, db.RedeemCouponParams{
		CouponID: c.ID, OrderID: orderID, UserID: userID,
		AmountCents: discountCents,
	}); err != nil {
		// Bound to the constraint NAME: pgconn renders a PgError as severity +
		// message + SQLSTATE, and the name is not in it.
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
			switch pgErr.ConstraintName {
			case "coupon_within_total_limit", "coupon_within_customer_limit":
				return coupon.ErrUsedUp
			case "coupon_is_current":
				return coupon.ErrExpired
			}
		}
		return fmt.Errorf("redeem coupon %s: %w", c.Code, err)
	}
	return nil
}
