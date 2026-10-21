//go:build integration

package checkout_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/checkout"
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

	// Through checkout, which redeems in the transaction that places the order.
	number, err := placeWithCoupon(t, checkout.NewStore(pool), code, 0)
	if err != nil {
		t.Fatalf("place with %s: %v", code, err)
	}

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
