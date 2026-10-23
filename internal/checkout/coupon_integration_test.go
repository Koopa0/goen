//go:build integration

package checkout_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/checkout/checkouttest"
	"github.com/koopa0/goen/internal/discount/coupon"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// insertCoupon inserts one and returns its code.
func insertCoupon(t *testing.T, code, kind string, amount, percent, minSpend int64, maxRedemptions int) string {
	t.Helper()
	var amountArg, percentArg, maxArg any
	if amount > 0 {
		amountArg = amount
	}
	if percent > 0 {
		percentArg = percent
	}
	if maxRedemptions > 0 {
		maxArg = maxRedemptions
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO coupons (code, description, kind, amount_cents, percent_bp,
		                     min_subtotal_cents, max_redemptions)
		VALUES ($1, '測試折扣', $2, $3, $4, $5, $6)`,
		code, kind, amountArg, percentArg, minSpend, maxArg); err != nil {
		t.Fatalf("create coupon %s: %v", code, err)
	}
	return code
}

// TestStore_PlaceOrderPercentCoupon: PlaceOrder with a 15% coupon gives a
// whole-NT$ discount and amount owed, and the invoice the issue claim files
// equals the capture. It places an order on a price where 15% leaves cents,
// captures its amount owed, and files the invoice through the real claim: the
// order total, the capture and the invoice amount are one whole NT$ figure.
func TestStore_PlaceOrderPercentCoupon(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	code := insertCoupon(t, "PCT15", "percent", 0, 1500, 0, 0)

	number, err := placeWithCoupon(t, s, code, 0)
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	var orderID uuid.UUID
	var discount, owed int64
	if err = pool.QueryRow(ctx, `
		SELECT id, discount_cents, order_amount_after_credit(id) FROM orders WHERE order_number = $1`,
		number).Scan(&orderID, &discount, &owed); err != nil {
		t.Fatalf("read: %v", err)
	}
	// 15% of NT$1,999 is NT$299.85; the fixture price is 199900 cents.
	if discount != 30000 {
		t.Errorf("discount = %d, want 30000", discount)
	}
	if owed%100 != 0 {
		t.Errorf("the amount owed, %d, is not a whole NT$", owed)
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO payments (order_id, provider, provider_ref, intended_amount_cents,
		                      captured_amount_cents, status, paid_at)
		VALUES ($1, 'stripe', 'cs_round_' || $2, $3, $3, 'succeeded', now())`,
		orderID, uuid.NewString()[:8], owed); err != nil {
		t.Fatalf("capture: %v", err)
	}

	var actorID uuid.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO users (email, role) VALUES ($1, 'staff') RETURNING id`,
		"round-"+uuid.NewString()+"@goen.invalid").Scan(&actorID); err != nil {
		t.Fatalf("staff actor: %v", err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err = conn.Exec(ctx, `SET ROLE admin`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `RESET ROLE`) }()
	var opID uuid.UUID
	if err = conn.QueryRow(ctx, `SELECT claim_invoice_issue($1, $2, $3)`,
		number, actorID, "round:"+uuid.NewString()).Scan(&opID); err != nil {
		t.Fatalf("claim the invoice: %v", err)
	}
	var invoiced, captured int64
	if err = pool.QueryRow(ctx, `
		SELECT io.amount_cents,
		       (SELECT sum(captured_amount_cents) FROM payments
		        WHERE order_id = io.order_id AND status = 'succeeded')
		FROM invoice_operations io WHERE io.id = $1`, opID).Scan(&invoiced, &captured); err != nil {
		t.Fatalf("read the invoice operation: %v", err)
	}
	if invoiced != captured {
		t.Errorf("the invoice is filed for %d but %d was captured", invoiced, captured)
	}
}

// TestStore_CouponByCodeDisabledAsUnknown: CouponByCode answers a disabled
// coupon exactly as it answers an unknown one, coupon.ErrNotFound. Telling
// somebody a code exists but is switched off confirms which of a shop's codes
// are real.
func TestStore_CouponByCodeDisabledAsUnknown(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	insertCoupon(t, "SWITCHEDOFF", "amount", 20000, 0, 0, 0)
	if _, err := pool.Exec(ctx, `UPDATE coupons SET is_active = false WHERE code = 'SWITCHEDOFF'`); err != nil {
		t.Fatalf("disable: %v", err)
	}

	_, offErr := s.CouponByCode(ctx, "SWITCHEDOFF")
	_, unknownErr := s.CouponByCode(ctx, "NEVEREXISTED")
	if !errors.Is(offErr, coupon.ErrNotFound) || !errors.Is(unknownErr, coupon.ErrNotFound) {
		t.Errorf("disabled gave %v and unknown gave %v; both must be coupon.ErrNotFound",
			offErr, unknownErr)
	}
}

// TestStore_CouponByCodeCase: CouponByCode finds a coupon whatever case its
// code is typed in and with spaces around it. A code is read off a card by a
// human.
func TestStore_CouponByCodeCase(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	insertCoupon(t, "MixedCase", "amount", 20000, 0, 0, 0)

	for _, typed := range []string{"MixedCase", "mixedcase", "MIXEDCASE", "  MixedCase  "} {
		if _, err := s.CouponByCode(ctx, typed); err != nil {
			t.Errorf("%q was not found: %v", typed, err)
		}
	}
}

// TestStore_PlaceOrderRedemptionLimit: PlaceOrder redeems a coupon limited to
// two redemptions for two orders and refuses the rest. The limit is counted
// from coupon_redemptions under a lock, never from a counter two checkouts
// could read.
func TestStore_PlaceOrderRedemptionLimit(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	code := insertCoupon(t, "ONLYTWO", "amount", 10000, 0, 0, 2)

	placed := 0
	var lastErr error
	for i := range 4 {
		_, err := s.CouponByCode(ctx, code)
		if err != nil {
			lastErr = err
			continue
		}
		if _, err := placeWithCoupon(t, s, code, i); err != nil {
			lastErr = err
			continue
		}
		placed++
	}
	if placed != 2 {
		t.Errorf("%d orders redeemed a coupon limited to 2 (last error: %v)", placed, lastErr)
	}

	var redemptions int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM coupon_redemptions cr JOIN coupons c ON c.id = cr.coupon_id
		WHERE c.code = $1`, code).Scan(&redemptions); err != nil {
		t.Fatalf("count: %v", err)
	}
	if redemptions != 2 {
		t.Errorf("%d redemptions recorded, want 2", redemptions)
	}
}

// TestStore_PlaceOrderRedemptionAmount: The redemption PlaceOrder records
// equals the discount of the order, and the coupon takes something off. It
// proves the two numbers are one fact.
func TestStore_PlaceOrderRedemptionAmount(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	code := insertCoupon(t, "MATCHES", "amount", 20000, 0, 0, 0)

	_, err := s.CouponByCode(ctx, code)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	number, err := placeWithCoupon(t, s, code, 0)
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	var discount, redeemed int64
	if err := pool.QueryRow(ctx, `
		SELECT o.discount_cents, cr.amount_cents
		FROM orders o JOIN coupon_redemptions cr ON cr.order_id = o.id
		WHERE o.order_number = $1`, number).Scan(&discount, &redeemed); err != nil {
		t.Fatalf("read: %v", err)
	}
	if discount != redeemed {
		t.Errorf("order discounted %d, redemption records %d", discount, redeemed)
	}
	if discount == 0 {
		t.Error("the coupon took nothing off")
	}
}

// TestStore_PlaceOrderCouponUsedUp: PlaceOrder refuses a spent coupon as
// coupon.ErrUsedUp, the sentinel the checkout handler branches on, though
// CouponByCode still lets it through. It holds the sentinel the checkout
// handler branches on; anything else is a 500 with the checkout form discarded.
// CouponByCode reads no limit, so this is not a race.
func TestStore_PlaceOrderCouponUsedUp(t *testing.T) {
	s := checkout.NewStore(pool)
	code := insertCoupon(t, "SPENTONCE", "amount", 10000, 0, 0, 1)

	_, err := s.CouponByCode(t.Context(), code)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if _, placeErr := placeWithCoupon(t, s, code, 1); placeErr != nil {
		t.Fatalf("the first order should have gone through: %v", placeErr)
	}

	// The pre-check passes again — that is the point.
	_, err = s.CouponByCode(t.Context(), code)
	if err != nil {
		t.Fatalf("a spent coupon must still pass the field validation, "+
			"or this test is not exercising the transactional refusal: %v", err)
	}

	_, err = placeWithCoupon(t, s, code, 2)
	if !errors.Is(err, coupon.ErrUsedUp) {
		t.Fatalf("redeeming a spent coupon = %v, want coupon.ErrUsedUp — the "+
			"checkout handler branches on this sentinel, and anything else is a "+
			"500 with the customer's address discarded", err)
	}
}

// placeWithCoupon places an order carrying a coupon and returns its number. Its
// own variant per call, because placing an order consumes stock and a shared
// seeded row fails under -shuffle.
func placeWithCoupon(t *testing.T, s *checkout.Store, code string, n int) (string, error) {
	t.Helper()
	ctx := t.Context()

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	vid := ordertest.FreshVariant(t, pool, "coupon-order")
	id := checkouttest.NewCart(t, s)
	if err := s.Carts().Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	addr := &delivery.Details{
		Email: "cp@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "路 1 號",
	}
	quote := checkouttest.Quote(t, s, id, uuid.NullUUID{}, shipID, addr, code)
	return s.PlaceOrder(ctx, id, uuid.NullUUID{}, shipID, addr, nil, code, quote,
		checkouttest.AttemptKey("coupon-test-"+strconv.Itoa(n)+"-"+uuid.NewString()))
}

// TestCouponsAmountPositive: The money ceiling coupons_amount_positive allows,
// times the largest basis-point percentage, stays inside the range where
// integer and float64 arithmetic agree. The discount is int64 and exact, and it
// agrees with float64 only while the largest product the schema allows, 10^10
// cents by 10^4 basis points, stays inside 2^53.
func TestCouponsAmountPositive(t *testing.T) {
	// Read from the schema rather than restated: a literal here would pass after
	// somebody raised it.
	var ceiling int64
	if err := pool.QueryRow(t.Context(), `
		-- PostgreSQL renders the literal quoted and cast: <= '10000000000'::bigint
		SELECT substring(pg_get_constraintdef(oid) from '<= ''([0-9]+)''')::bigint
		FROM pg_constraint WHERE conname = 'coupons_amount_positive'`).Scan(&ceiling); err != nil {
		t.Fatalf("read the money ceiling from the schema: %v", err)
	}
	if ceiling == 0 {
		t.Fatal("could not read the ceiling; the constraint's shape changed")
	}

	const maxBasisPoints = 10000
	const float64ExactMax = int64(1) << 53

	product := ceiling * maxBasisPoints
	if product > float64ExactMax {
		t.Errorf("the largest discount product is %d, past float64's exact range of %d.\n"+
			"Integer arithmetic is still correct, but the comments claiming int and "+
			"float agree here are now wrong — go read them.", product, float64ExactMax)
	}
}

// TestRedeemCouponCancelledSlot: redeem_coupon counts a pending order's
// redemption against the limit and gives the slot back once the order is
// cancelled. coupon_redemptions is append-only with every write revoked, so a
// slot a cancelled checkout still consumed could be freed by no door. A PENDING
// unpaid order still counts.
func TestRedeemCouponCancelledSlot(t *testing.T) {
	ctx := t.Context()

	var couponID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO coupons (code, description, kind, amount_cents, max_redemptions, per_customer_limit)
		VALUES ('ONESHOT', '只能用一次', 'amount', 20000, 1, 1)
		RETURNING id`).Scan(&couponID); err != nil {
		t.Fatalf("create coupon: %v", err)
	}

	var placed []uuid.UUID
	// ONE transaction, as checkout places an order and redeems on it:
	// orders_have_lines is deferred, and coupon_redemptions_written_while_placing
	// takes no redemption from a later one. The redemption's refusal is
	// returned, and refuses the order with it.
	place := func(t *testing.T) error {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()

		var orderID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
			                    shipping_method_name, shipping_cents, discount_cents)
			SELECT next_order_number(), v.id, sm.code, v.name, 0, 20000
			FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
			ORDER BY v.effective_at LIMIT 1
			RETURNING id`).Scan(&orderID); err != nil {
			t.Fatalf("create order: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
			VALUES ($1, 'CPN-SLOT', '測試商品', 50000, 1)`, orderID); err != nil {
			t.Fatalf("create order line: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_private_data (order_id, email, recipient_name, phone,
			                                postal_code, city, district, street)
			VALUES ($1, 'slot@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
			orderID); err != nil {
			t.Fatalf("create private data: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT redeem_coupon($1, $2, NULL, 20000)`,
			couponID, orderID); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
		placed = append(placed, orderID)
		return nil
	}

	if err := place(t); err != nil {
		t.Fatalf("first redemption: %v", err)
	}
	first := placed[0]

	// While it is still pending, the slot is taken.
	if err := place(t); err == nil {
		t.Fatal("a pending order's redemption did not hold the last slot — two " +
			"concurrent checkouts would both be allowed past it")
	}

	// Cancelling gives it back.
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now() WHERE id = $1`,
		first); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if err := place(t); err != nil {
		t.Errorf("the slot is still consumed after the order was cancelled: %v — "+
			"coupon_redemptions is append-only and no role may delete one, so this "+
			"is the only door there is", err)
	}
}

// TestStore_CouponByCodeWindow: CouponByCode judges a coupon's window against
// the database's clock: a coupon created this instant is usable, and one that
// has not started or has ended answers coupon.ErrExpired. starts_at defaults to
// the DATABASE's now(), so comparing it against Go's time.Now() is comparing
// two clocks.
func TestStore_CouponByCodeWindow(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)

	// Created this instant, with the schema's own default start.
	if _, err := pool.Exec(ctx, `
		INSERT INTO coupons (code, description, kind, amount_cents)
		VALUES ('JUSTNOW', '剛剛建立', 'amount', 20000)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.CouponByCode(ctx, "JUSTNOW"); err != nil {
		t.Errorf("a coupon created this instant is not usable: %v — the window is "+
			"being judged against a different clock from the one that set it", err)
	}

	// The bounds still bind.
	if _, err := pool.Exec(ctx, `
		INSERT INTO coupons (code, description, kind, amount_cents, starts_at, ends_at)
		VALUES ('FUTURE', '還沒開始', 'amount', 20000,
		        now() + interval '1 day', now() + interval '2 days')`); err != nil {
		t.Fatalf("create future: %v", err)
	}
	if _, err := s.CouponByCode(ctx, "FUTURE"); !errors.Is(err, coupon.ErrExpired) {
		t.Errorf("a coupon that has not started gave %v, want coupon.ErrExpired", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO coupons (code, description, kind, amount_cents, starts_at, ends_at)
		VALUES ('LAPSED', '已經結束', 'amount', 20000,
		        now() - interval '2 days', now() - interval '1 day')`); err != nil {
		t.Fatalf("create lapsed: %v", err)
	}
	if _, err := s.CouponByCode(ctx, "LAPSED"); !errors.Is(err, coupon.ErrExpired) {
		t.Errorf("a lapsed coupon gave %v, want coupon.ErrExpired", err)
	}
}
