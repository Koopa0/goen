//go:build integration

package checkouttest_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/checkout/checkouttest"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// scalar reads one value, so each test states the state it expects in one line.
func scalar[T any](t *testing.T, query string, args ...any) T {
	t.Helper()
	var v T
	if err := pool.QueryRow(t.Context(), query, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return v
}

// TestPlaceUnpaidOrderFor holds that the order went through the real checkout:
// pending, addressed to the email, with its line and its hold written by the
// placing transaction.
func TestPlaceUnpaidOrderFor(t *testing.T) {
	number := checkouttest.PlaceUnpaidOrderFor(t, pool, checkout.NewStore(pool), "placed@example.com")

	if got := scalar[string](t, `SELECT fulfillment_status FROM orders WHERE order_number = $1`, number); got != "pending" {
		t.Errorf("status = %q, want pending", got)
	}
	if got := scalar[string](t, `
		SELECT d.email FROM order_private_data d JOIN orders o ON o.id = d.order_id
		WHERE o.order_number = $1`, number); got != "placed@example.com" {
		t.Errorf("email = %q", got)
	}
	if got := scalar[int](t, `
		SELECT count(*) FROM inventory_reservations r JOIN orders o ON o.id = r.order_id
		WHERE o.order_number = $1 AND r.state = 'held'`, number); got != 1 {
		t.Errorf("%d holds, want 1", got)
	}
}

// TestPlaceOrderOfUnits holds that the line carries the units asked for.
func TestPlaceOrderOfUnits(t *testing.T) {
	number := checkouttest.PlaceOrderOfUnits(t, pool, checkout.NewStore(pool), "units@example.com", 3)

	if got := scalar[int32](t, `
		SELECT l.quantity FROM order_lines l JOIN orders o ON o.id = l.order_id
		WHERE o.order_number = $1`, number); got != 3 {
		t.Errorf("quantity = %d, want 3", got)
	}
}

// TestPlaceOrder holds that a cart placed with the quote a direct Store test
// would have rendered becomes an order, in a cart that came with its token.
func TestPlaceOrder(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	id, token := checkouttest.NewCartSession(t, s)
	if token == "" {
		t.Fatal("NewCartSession returned no token")
	}
	if err := s.Carts().Add(ctx, id, ordertest.FreshVariant(t, pool, "place-order"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	shipID := scalar[uuid.UUID](t, `SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`)

	number, err := checkouttest.PlaceOrder(t, s, ctx, id, uuid.NullUUID{}, shipID, deliveryTo("place@example.com"), "",
		"place-order-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	if got := scalar[int](t, `SELECT count(*) FROM orders WHERE order_number = $1`, number); got != 1 {
		t.Errorf("%d orders numbered %s, want 1", got, number)
	}
}

// TestPlaceWithCoupon holds that the order redeemed the coupon in the placing
// transaction and was discounted by its amount.
func TestPlaceWithCoupon(t *testing.T) {
	code := "SMOKE" + strings.ToUpper(uuid.NewString()[:6])
	couponID := scalar[uuid.UUID](t, `
		INSERT INTO coupons (code, description, kind, amount_cents)
		VALUES ($1, '煙霧測試', 'amount', 20000) RETURNING id`, code)

	number, err := checkouttest.PlaceWithCoupon(t, pool, checkout.NewStore(pool), code, 0)
	if err != nil {
		t.Fatalf("place with %s: %v", code, err)
	}
	if got := scalar[int](t, `
		SELECT count(*) FROM coupon_redemptions c JOIN orders o ON o.id = c.order_id
		WHERE o.order_number = $1 AND c.coupon_id = $2`, number, couponID); got != 1 {
		t.Errorf("%d redemptions of the coupon, want 1", got)
	}
	if got := scalar[int64](t, `SELECT discount_cents FROM orders WHERE order_number = $1`, number); got != 20000 {
		t.Errorf("discount = %d cents, want 20000", got)
	}
}

// TestAttemptKey holds that the key is a fixed-size identity, and that a key
// already in that form comes back as it was.
func TestAttemptKey(t *testing.T) {
	key := checkouttest.AttemptKey("a readable label")

	if len(key) != 22 {
		t.Errorf("key %q has %d characters, want 22 (16 bytes, unpadded base64)", key, len(key))
	}
	if again := checkouttest.AttemptKey(key); again != key {
		t.Errorf("AttemptKey of a key = %q, want it unchanged (%q)", again, key)
	}
}

// TestCreditedAccount holds that the account's ledger sums to the credit asked
// for.
func TestCreditedAccount(t *testing.T) {
	_, accountID := checkouttest.CreditedAccount(t, pool, 7000)

	if got := checkouttest.CreditBalance(t, pool, accountID); got != 7000 {
		t.Errorf("CreditBalance = %d, want 7000", got)
	}
}

// TestPlaceLapsed holds that the order spent its credit and its single coupon
// slot in the placing transaction, and that every hold is past its expiry.
func TestPlaceLapsed(t *testing.T) {
	number, accountID, couponID := checkouttest.PlaceLapsed(t, pool, "placed-lapsed")

	if got := checkouttest.CreditBalance(t, pool, accountID); got != 0 {
		t.Errorf("balance = %d, want 0: the order spent the credit", got)
	}
	if got := scalar[int](t, `
		SELECT count(*) FROM coupon_redemptions c JOIN orders o ON o.id = c.order_id
		WHERE o.order_number = $1 AND c.coupon_id = $2`, number, couponID); got != 1 {
		t.Errorf("%d redemptions of the coupon, want 1", got)
	}
	if got := scalar[int](t, `
		SELECT count(*) FROM inventory_reservations r JOIN orders o ON o.id = r.order_id
		WHERE o.order_number = $1 AND r.state = 'held' AND r.expires_at < now()`, number); got != 1 {
		t.Errorf("%d expired holds, want 1", got)
	}
}

func deliveryTo(email string) *delivery.Details {
	return &delivery.Details{
		Email: email, RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
}
