//go:build integration

package buyer_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/checkout/checkouttest"
	"github.com/koopa0/goen/internal/discount/coupon"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// TestStore_OrderDiscountReason: Store.Order says why an order
// placed through checkout was discounted: the coupon's code, its
// description and its kind.
func TestStore_OrderDiscountReason(t *testing.T) {
	ctx := t.Context()
	s := buyer.NewStore(pool)

	code := "SAVE" + strings.ToUpper(uuid.NewString()[:6])
	if _, err := pool.Exec(ctx, `
		INSERT INTO coupons (code, description, kind, amount_cents)
		VALUES ($1, '滿額折抵', 'amount', 20000)`, code); err != nil {
		t.Fatalf("create coupon: %v", err)
	}

	// Through checkout, which redeems in the transaction that places the order.
	number, err := checkouttest.PlaceWithCoupon(t, pool, checkout.NewStore(pool), code, 0)
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
	if want := (coupon.Redeemed{Code: code, Description: "滿額折抵", Kind: coupon.Amount}); view.Coupon != want {
		t.Errorf("Coupon = %+v, want %+v", view.Coupon, want)
	}
}

// TestStore_OrderNoDiscountReason: Store.Order reports no discount
// reason for an order with no coupon.
func TestStore_OrderNoDiscountReason(t *testing.T) {
	ctx := t.Context()
	s := buyer.NewStore(pool)
	number := ordertest.OrderOfVariants(t, pool, map[uuid.UUID]int32{ordertest.FreshVariant(t, pool, "no-coupon"): 1})

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if view.Coupon.Used() {
		t.Errorf("an order with no coupon reports %+v, want none", view.Coupon)
	}
}
