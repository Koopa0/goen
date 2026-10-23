//go:build integration

package cart_test

import (
	"testing"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// TestStore_ViewCheckoutWindows: The cart and checkout pages state the hold and
// the start window from the view the store builds; those are the schema's.
func TestStore_ViewCheckoutWindows(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	if err := s.Add(ctx, id, ordertest.FreshVariant(t, pool, "stated-windows"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	view, err := s.View(ctx, id)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	var hold, payStart int64
	if err := pool.QueryRow(ctx, `SELECT checkout_hold_minutes(), checkout_pay_start_minutes()`).
		Scan(&hold, &payStart); err != nil {
		t.Fatalf("read the checkout windows: %v", err)
	}
	if view.HoldMinutes != hold || view.PayStartMinutes != payStart {
		t.Errorf("the cart states a %d-minute hold and %d minutes to start paying; the schema names %d and %d",
			view.HoldMinutes, view.PayStartMinutes, hold, payStart)
	}
}
