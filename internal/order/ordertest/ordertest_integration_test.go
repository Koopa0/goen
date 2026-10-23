//go:build integration

package ordertest_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

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

// TestHeldOrder holds that a guest's order holds one unit of its variant on a
// hold that has already run out, and that it is paid only when asked.
func TestHeldOrder(t *testing.T) {
	for _, paid := range []bool{false, true} {
		vid := ordertest.FreshVariant(t, pool, "held-order")
		id := ordertest.HeldOrder(t, pool, vid, time.Hour, paid)

		if owner := scalar[*uuid.UUID](t, `SELECT user_id FROM orders WHERE id = $1`, id); owner != nil {
			t.Errorf("paid=%v: owner = %v, want a guest", paid, *owner)
		}
		if held := scalar[int](t, `
			SELECT count(*) FROM inventory_reservations
			WHERE order_id = $1 AND variant_id = $2 AND state = 'held' AND quantity = 1
			  AND expires_at < now()`, id, vid); held != 1 {
			t.Errorf("paid=%v: %d expired holds of the variant, want 1", paid, held)
		}
		if got := scalar[int](t, `SELECT count(*) FROM payments WHERE order_id = $1 AND status = 'succeeded'`, id); got != btoi(paid) {
			t.Errorf("paid=%v: %d succeeded payments, want %d", paid, got, btoi(paid))
		}
		if got := ordertest.NumberOf(t, pool, id); got != scalar[string](t, `SELECT order_number FROM orders WHERE id = $1`, id) {
			t.Errorf("paid=%v: NumberOf = %q", paid, got)
		}
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestHeldOrderFor holds that the order belongs to the owner it was given.
func TestHeldOrderFor(t *testing.T) {
	owner := ordertest.Customer(t, pool)
	id := ordertest.HeldOrderFor(t, pool, uuid.NullUUID{UUID: owner, Valid: true},
		ordertest.FreshVariant(t, pool, "held-order-for"), time.Hour, false)

	if got := scalar[uuid.UUID](t, `SELECT user_id FROM orders WHERE id = $1`, id); got != owner {
		t.Errorf("owner = %v, want %v", got, owner)
	}
}

// TestCreditFundedHeldOrder holds that store credit paid the whole order, so no
// card payment exists and the customer's balance is spent, and that the hold has
// run out.
func TestCreditFundedHeldOrder(t *testing.T) {
	vid := ordertest.FreshVariant(t, pool, "credit-funded")
	id := ordertest.CreditFundedHeldOrder(t, pool, vid, time.Hour)

	if got := scalar[int](t, `SELECT count(*) FROM payments WHERE order_id = $1`, id); got != 0 {
		t.Errorf("%d payments, want none: credit paid the order", got)
	}
	if spent := scalar[int64](t, `
		SELECT coalesce(sum(amount_cents), 0) FROM store_credit_entries WHERE order_id = $1`, id); spent != -100000 {
		t.Errorf("credit spent on the order = %d, want -100000", spent)
	}
	if left := scalar[int64](t, `
		SELECT coalesce(sum(e.amount_cents), 0)
		FROM store_credit_entries e JOIN store_credit_accounts a ON a.id = e.account_id
		JOIN orders o ON o.user_id = a.user_id WHERE o.id = $1`, id); left != 0 {
		t.Errorf("balance left = %d, want 0", left)
	}
	if held := scalar[int](t, `
		SELECT count(*) FROM inventory_reservations
		WHERE order_id = $1 AND state = 'held' AND expires_at < now()`, id); held != 1 {
		t.Errorf("%d expired holds, want 1", held)
	}
}

// TestCreditedCustomer holds that the customer starts with the credit asked for.
func TestCreditedCustomer(t *testing.T) {
	id := ordertest.CreditedCustomer(t, pool, 5000)

	if got := scalar[int64](t, `
		SELECT coalesce(sum(e.amount_cents), 0)
		FROM store_credit_entries e JOIN store_credit_accounts a ON a.id = e.account_id
		WHERE a.user_id = $1`, id); got != 5000 {
		t.Errorf("credit = %d, want 5000", got)
	}
}

// TestVariantsOf holds that each variant is sellable on a product of its own,
// stocked with ten, and that two calls never share a product.
func TestVariantsOf(t *testing.T) {
	a, b, c := ordertest.VariantsOf(t, pool, "variants-of", 3)
	if a == b || b == c || a == c {
		t.Fatalf("variants %v, %v, %v are not three different variants", a, b, c)
	}
	for _, vid := range []uuid.UUID{a, b, c} {
		if got := ordertest.StockOf(t, pool, vid); got != 10 {
			t.Errorf("StockOf(%v) = %d, want 10", vid, got)
		}
		if active := scalar[bool](t, `
			SELECT pv.is_active AND p.status = 'active'
			FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $1`, vid); !active {
			t.Errorf("variant %v is not sellable", vid)
		}
	}
	if scalar[uuid.UUID](t, `SELECT product_id FROM product_variants WHERE id = $1`, a) !=
		scalar[uuid.UUID](t, `SELECT product_id FROM product_variants WHERE id = $1`, c) {
		t.Error("the variants of one call are on different products")
	}
	if other := ordertest.FreshVariant(t, pool, "variants-of"); scalar[uuid.UUID](t, `SELECT product_id FROM product_variants WHERE id = $1`, other) ==
		scalar[uuid.UUID](t, `SELECT product_id FROM product_variants WHERE id = $1`, a) {
		t.Error("two calls share a product")
	}
	x, y, z := ordertest.ThreeVariants(t, pool, "three-variants")
	if x == y || y == z || x == z {
		t.Errorf("ThreeVariants = %v, %v, %v, want three different variants", x, y, z)
	}
}

// TestOrderOfVariants holds that the committed order carries a line for each
// variant it was asked for, with that quantity.
func TestOrderOfVariants(t *testing.T) {
	a, b, _ := ordertest.ThreeVariants(t, pool, "order-of-variants")
	number := ordertest.OrderOfVariants(t, pool, map[uuid.UUID]int32{a: 2, b: 3})

	for vid, want := range map[uuid.UUID]int32{a: 2, b: 3} {
		if got := scalar[int32](t, `
			SELECT l.quantity FROM order_lines l JOIN orders o ON o.id = l.order_id
			WHERE o.order_number = $1 AND l.variant_id = $2`, number, vid); got != want {
			t.Errorf("quantity of %v = %d, want %d", vid, got, want)
		}
	}
}

// TestRefundedAndCancelled holds that a paid order ends refunded and cancelled.
func TestRefundedAndCancelled(t *testing.T) {
	id := ordertest.HeldOrder(t, pool, ordertest.FreshVariant(t, pool, "refunded"), -time.Hour, true)

	ordertest.RefundedAndCancelled(t, pool, id)

	if got := scalar[string](t, `SELECT fulfillment_status FROM orders WHERE id = $1`, id); got != "cancelled" {
		t.Errorf("status = %q, want cancelled", got)
	}
	if got := scalar[int](t, `
		SELECT count(*) FROM refunds r JOIN payments p ON p.id = r.payment_id
		WHERE p.order_id = $1 AND r.status = 'succeeded'`, id); got != 1 {
		t.Errorf("%d settled refunds, want 1", got)
	}
}

// TestPendingOrderHoldingStock holds that the order is pending and holds one
// unit of the variant it returns.
func TestPendingOrderHoldingStock(t *testing.T) {
	number, id, vid := ordertest.PendingOrderHoldingStock(t, pool)

	if got := scalar[string](t, `SELECT fulfillment_status FROM orders WHERE id = $1`, id); got != "pending" {
		t.Errorf("%s: status = %q, want pending", number, got)
	}
	if held := scalar[int](t, `
		SELECT count(*) FROM inventory_reservations
		WHERE order_id = $1 AND variant_id = $2 AND state = 'held' AND quantity = 1`, id, vid); held != 1 {
		t.Errorf("%d holds of the variant, want 1", held)
	}
}

// TestPaidPickingOrderForUser holds that the customer's order is paid, picking,
// and earned its points.
func TestPaidPickingOrderForUser(t *testing.T) {
	user := ordertest.Customer(t, pool)
	number, id := ordertest.PaidPickingOrderForUser(t, pool, user, 50000)

	if got := scalar[uuid.UUID](t, `SELECT user_id FROM orders WHERE order_number = $1`, number); got != user {
		t.Errorf("owner = %v, want %v", got, user)
	}
	if got := scalar[string](t, `SELECT fulfillment_status FROM orders WHERE id = $1`, id); got != "picking" {
		t.Errorf("status = %q, want picking", got)
	}
	if got := scalar[int64](t, `
		SELECT captured_amount_cents FROM payments WHERE order_id = $1 AND status = 'succeeded'`, id); got != 50000 {
		t.Errorf("paid = %d, want 50000", got)
	}
}

// TestCustomer holds that the customer is a customer with no order.
func TestCustomer(t *testing.T) {
	id := ordertest.Customer(t, pool)

	if got := scalar[string](t, `SELECT role FROM users WHERE id = $1`, id); got != "customer" {
		t.Errorf("role = %q, want customer", got)
	}
	if got := scalar[int](t, `SELECT count(*) FROM orders WHERE user_id = $1`, id); got != 0 {
		t.Errorf("%d orders, want none", got)
	}
}
