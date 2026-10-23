//go:build integration

package checkout_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/checkout/checkouttest"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// TestStore_PlaceOrderCreditAfterDiscount: PlaceOrder caps store credit at what
// the order owes after the discount, so a coupon and a balance never spend more
// credit than the order is worth. The cap is `subtotal - discount + shipping`:
// leaving the discount out lets a coupon and a balance spend more credit than
// the order is worth, which nothing refuses.
func TestStore_PlaceOrderCreditAfterDiscount(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)

	if _, err := pool.Exec(ctx, `
		INSERT INTO coupons (code, description, kind, amount_cents, min_subtotal_cents)
		VALUES ('CREDITCAP', '測試折抵', 'amount', 30000, 0)`); err != nil {
		t.Fatalf("create the coupon: %v", err)
	}
	userID := ordertest.CreditedCustomer(t, pool, 10000000) // NT$100,000 — far above the order

	id := checkouttest.NewCart(t, s)
	if err := s.Carts().Add(ctx, id, ordertest.FreshVariant(t, pool, "creditcap"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	_, err := s.CouponByCode(ctx, "CREDITCAP")
	if err != nil {
		t.Fatalf("find the coupon: %v", err)
	}

	number, err := checkouttest.PlaceOrder(t, s, ctx, id, uuid.NullUUID{UUID: userID, Valid: true},
		shipVersionFor(t, "home_delivery"), &delivery.Details{
			Email: "creditcap@example.com", RecipientName: "王小明", Phone: "0912345678",
			PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
		}, "CREDITCAP", "creditcap-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	var owed, debit int64
	if err := pool.QueryRow(ctx, `
		SELECT order_amount_after_credit(o.id),
		       -coalesce((SELECT sum(e.amount_cents) FROM store_credit_entries e
		                  WHERE e.order_id = o.id), 0)
		FROM orders o WHERE o.order_number = $1`, number).Scan(&owed, &debit); err != nil {
		t.Fatalf("read the order: %v", err)
	}

	if owed != 0 {
		t.Errorf("order_amount_after_credit(%s) = %d, want 0. A negative figure means credit was "+
			"spent against the GROSS total, which loses the customer the discount and "+
			"leaves the order permanently unpayable", number, owed)
	}
	if debit <= 0 {
		t.Errorf("credit debited %d on the order, want a positive spend", debit)
	}
}

// TestStore_PlaceOrderCreditSpent: PlaceOrder spends a customer's whole credit
// balance on an order that costs more, posting the debit against that order.
// The test forces no rollback, so it does not itself show that the spend
// commits or fails with the order.
func TestStore_PlaceOrderCreditSpent(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	vid := variantOf(t, "koto-over-ear")
	userID := ordertest.CreditedCustomer(t, pool, 300000) // NT$3,000

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &delivery.Details{
		Email: "c@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}

	id := checkouttest.NewCart(t, s)
	if err := s.Carts().Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	number, err := checkouttest.PlaceOrder(t, s, ctx, id,
		uuid.NullUUID{UUID: userID, Valid: true}, shipID, addr, "", "credit-1-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	var spent int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(e.amount_cents), 0) FROM store_credit_entries e
		JOIN orders o ON o.id = e.order_id
		WHERE o.order_number = $1`, number).Scan(&spent); err != nil {
		t.Fatalf("read spend: %v", err)
	}
	if spent != -300000 {
		t.Errorf("credit spent is %d, want -300000 — the whole balance, since the "+
			"order costs more than it", spent)
	}

	var balance int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(e.amount_cents), 0) FROM store_credit_entries e
		JOIN store_credit_accounts a ON a.id = e.account_id
		WHERE a.user_id = $1`, userID).Scan(&balance); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if balance != 0 {
		t.Errorf("balance is %d after spending it all, want 0", balance)
	}
}

// TestStore_PlaceOrderCreditCapped: PlaceOrder never spends more credit than
// the order owes, and a fully credited order enters fulfilment with no payment
// row.
func TestStore_PlaceOrderCreditCapped(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	vid := variantOf(t, "koto-over-ear")
	userID := ordertest.CreditedCustomer(t, pool, 100000000) // NT$1,000,000: far more than any order

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &delivery.Details{
		Email: "c2@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}

	id := checkouttest.NewCart(t, s)
	if err := s.Carts().Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	number, err := checkouttest.PlaceOrder(t, s, ctx, id,
		uuid.NullUUID{UUID: userID, Valid: true}, shipID, addr, "", "credit-2-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	var spent, total int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(e.amount_cents), 0),
		       (SELECT coalesce(sum(ol.unit_price_cents * ol.quantity), 0) + o.shipping_cents
		        FROM order_lines ol WHERE ol.order_id = o.id)
		FROM orders o
		LEFT JOIN store_credit_entries e ON e.order_id = o.id
		WHERE o.order_number = $1
		GROUP BY o.id, o.shipping_cents`, number).Scan(&spent, &total); err != nil {
		t.Fatalf("read: %v", err)
	}
	if -spent != total {
		t.Errorf("spent %d against an order owing %d; credit must be capped at the total",
			-spent, total)
	}

	var orderID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM orders WHERE order_number = $1`, number).Scan(&orderID); err != nil {
		t.Fatalf("find order: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("a fully-credited order could not enter fulfilment: %v", err)
	}

	var payments int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE order_id = $1`, orderID).Scan(&payments); err != nil {
		t.Fatalf("count payments: %v", err)
	}
	if payments != 0 {
		t.Errorf("%d payment rows on a fully-credited order, want 0", payments)
	}
}

// TestStore_PlaceOrderGuestNoCredit: PlaceOrder for a guest posts no
// store-credit entry.
func TestStore_PlaceOrderGuestNoCredit(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	vid := variantOf(t, "koto-over-ear")

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &delivery.Details{
		Email: "g@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	id := checkouttest.NewCart(t, s)
	if err := s.Carts().Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	number, err := checkouttest.PlaceOrder(t, s, ctx, id, uuid.NullUUID{}, shipID, addr, "", "credit-guest-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	var entries int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM store_credit_entries e JOIN orders o ON o.id = e.order_id
		WHERE o.order_number = $1`, number).Scan(&entries); err != nil {
		t.Fatalf("count: %v", err)
	}
	if entries != 0 {
		t.Errorf("%d credit entries on a GUEST order, want 0", entries)
	}
}
