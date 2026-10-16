//go:build integration

package cart_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/order/buyer"
)

func TestAnOrderSaysWhyItWasDiscounted(t *testing.T) {
	ctx := t.Context()
	s := buyer.NewStore(pool)

	code := "SAVE" + strings.ToUpper(uuid.NewString()[:6])
	if _, err := pool.Exec(ctx, `
		INSERT INTO coupons (code, description, kind, amount_cents)
		VALUES ($1, '滿額折抵', 'amount', 20000)`, code); err != nil {
		t.Fatalf("create coupon: %v", err)
	}

	number := orderWithCoupon(t, code)

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if view.DiscountCents != 20000 {
		t.Fatalf("discount is %d, want 20000", view.DiscountCents)
	}
	if !strings.Contains(view.DiscountReason, code) {
		t.Errorf("the order does not say which coupon: %q", view.DiscountReason)
	}
	if !strings.Contains(view.DiscountReason, "滿額折抵") {
		t.Errorf("the order does not say what the coupon was for: %q", view.DiscountReason)
	}
}

func TestAnOrderWithNoCouponHasNoReason(t *testing.T) {
	ctx := t.Context()
	s := buyer.NewStore(pool)
	number := orderOfVariants(t, map[uuid.UUID]int32{freshVariant(t, "no-coupon"): 1})

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if view.DiscountReason != "" {
		t.Errorf("an order with no coupon reports %q, want empty", view.DiscountReason)
	}
}

// orderWithCoupon places an order that redeems a coupon, and returns its number.
func orderWithCoupon(t *testing.T, code string) string {
	t.Helper()
	ctx := t.Context()
	vid := freshVariant(t, "coupon-order")
	number := orderOfVariants(t, map[uuid.UUID]int32{vid: 1})

	var orderID, couponID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM orders WHERE order_number = $1`, number).Scan(&orderID); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM coupons WHERE upper(code) = upper($1)`, code).Scan(&couponID); err != nil {
		t.Fatalf("read coupon: %v", err)
	}
	// The discount and the redemption together: coupon_redemption_matches_order
	// holds them to each other.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET discount_cents = 20000 WHERE id = $1`, orderID); err != nil {
		t.Fatalf("set the discount: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT redeem_coupon($1, $2, NULL, 20000)`, couponID, orderID); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number
}
