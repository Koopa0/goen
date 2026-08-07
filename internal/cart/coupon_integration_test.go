//go:build integration

package cart_test

import (
	"errors"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
)

// coupon inserts one and returns its code.
func coupon(t *testing.T, code, kind string, amount, percent, cap_, minSpend int64, maxRedemptions int) string {
	t.Helper()
	var amountArg, percentArg, capArg, maxArg any
	if amount > 0 {
		amountArg = amount
	}
	if percent > 0 {
		percentArg = percent
	}
	if cap_ > 0 {
		capArg = cap_
	}
	if maxRedemptions > 0 {
		maxArg = maxRedemptions
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO coupons (code, description, kind, amount_cents, percent_bp,
		                     max_discount_cents, min_subtotal_cents, max_redemptions)
		VALUES ($1, '測試折扣', $2, $3, $4, $5, $6, $7)`,
		code, kind, amountArg, percentArg, capArg, minSpend, maxArg); err != nil {
		t.Fatalf("create coupon %s: %v", code, err)
	}
	return code
}

// TestCouponPricing is the arithmetic, and every case is a hand-computed
// literal rather than a re-run of the code under test.
func TestCouponPricing(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	coupon(t, "FLAT200", "amount", 20000, 0, 0, 0, 0)
	coupon(t, "PCT20", "percent", 0, 2000, 0, 0, 0)
	coupon(t, "PCT20CAP", "percent", 0, 2000, 50000, 0, 0)
	coupon(t, "SHIP", "free_shipping", 0, 0, 0, 0, 0)
	coupon(t, "BIG", "amount", 900000, 0, 0, 0, 0)

	tests := []struct {
		name             string
		code             string
		subtotal, ship   int64
		wantDiscount     int64
		wantFreeShipping bool
	}{
		{"flat amount", "FLAT200", 500000, 8000, 20000, false},
		{"twenty percent", "PCT20", 500000, 8000, 100000, false},
		{"capped percent", "PCT20CAP", 500000, 8000, 50000, false},
		{"percent under the cap", "PCT20CAP", 100000, 8000, 20000, false},
		{"free shipping is not a discount", "SHIP", 500000, 8000, 0, true},
		// The discount may never exceed the subtotal: a coupon worth more than
		// the goods would eat the shipping fee and drive the total negative,
		// which orders_total_non_negative refuses — a failed checkout instead
		// of a correct one.
		{"coupon larger than the order", "BIG", 100000, 8000, 100000, false},
		// 33% of 1001 cents truncates to 330, not 330.33. Integer throughout.
		{"truncation favours the customer", "PCT20", 1001, 0, 200, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := s.FindCoupon(ctx, tt.code, tt.subtotal, tt.ship)
			if err != nil {
				t.Fatalf("find %s: %v", tt.code, err)
			}
			if c.DiscountCents != tt.wantDiscount {
				t.Errorf("discount on %d is %d, want %d", tt.subtotal, c.DiscountCents, tt.wantDiscount)
			}
			if c.FreeShipping != tt.wantFreeShipping {
				t.Errorf("free shipping is %v, want %v", c.FreeShipping, tt.wantFreeShipping)
			}
		})
	}
}

// TestCouponMinimumSpend proves the threshold binds at its own boundary.
func TestCouponMinimumSpend(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	coupon(t, "MIN1000", "amount", 20000, 0, 0, 100000, 0)

	if _, err := s.FindCoupon(ctx, "MIN1000", 99999, 0); !errors.Is(err, cart.ErrCouponMinimum) {
		t.Errorf("below the minimum gave %v, want ErrCouponMinimum", err)
	}
	// Exactly the minimum qualifies — the boundary, which is where an
	// off-by-one lives.
	if _, err := s.FindCoupon(ctx, "MIN1000", 100000, 0); err != nil {
		t.Errorf("exactly the minimum was refused: %v", err)
	}
}

// TestUnknownAndDisabledCouponsLookTheSame proves a switched-off code is
// indistinguishable from one that never existed.
//
// A disabled coupon reads as an unknown one on purpose: telling somebody
// "this code exists but is switched off" confirms which of a shop's codes are
// real to anyone guessing.
func TestUnknownAndDisabledCouponsLookTheSame(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	coupon(t, "SWITCHEDOFF", "amount", 20000, 0, 0, 0, 0)
	if _, err := pool.Exec(ctx, `UPDATE coupons SET is_active = false WHERE code = 'SWITCHEDOFF'`); err != nil {
		t.Fatalf("disable: %v", err)
	}

	_, offErr := s.FindCoupon(ctx, "SWITCHEDOFF", 500000, 0)
	_, unknownErr := s.FindCoupon(ctx, "NEVEREXISTED", 500000, 0)
	if !errors.Is(offErr, cart.ErrNoSuchCoupon) || !errors.Is(unknownErr, cart.ErrNoSuchCoupon) {
		t.Errorf("disabled gave %v and unknown gave %v; both must be ErrNoSuchCoupon",
			offErr, unknownErr)
	}
}

// TestCouponCodeIsCaseInsensitive proves the lookup matches what a human typed.
// A code is read off a card, not copied by a machine.
func TestCouponCodeIsCaseInsensitive(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	coupon(t, "MixedCase", "amount", 20000, 0, 0, 0, 0)

	for _, typed := range []string{"MixedCase", "mixedcase", "MIXEDCASE", "  MixedCase  "} {
		if _, err := s.FindCoupon(ctx, typed, 500000, 0); err != nil {
			t.Errorf("%q was not found: %v", typed, err)
		}
	}
}

// TestTotalRedemptionLimitIsEnforced is the one a shop loses money on.
//
// The limit is counted from coupon_redemptions under a lock on the coupon row,
// never from a counter two checkouts could each read and each increment.
func TestTotalRedemptionLimitIsEnforced(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	code := coupon(t, "ONLYTWO", "amount", 10000, 0, 0, 0, 2)

	placed := 0
	var lastErr error
	for i := range 4 {
		c, err := s.FindCoupon(ctx, code, 500000, 0)
		if err != nil {
			lastErr = err
			continue
		}
		if _, err := placeWithCoupon(t, s, c, i); err != nil {
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

// TestTheRedemptionMatchesTheOrdersDiscount proves the two numbers are one fact.
//
// The row and orders.discount_cents are one fact. Two independent numbers is
// how a shop ends up unable to say what an order was actually given.
func TestTheRedemptionMatchesTheOrdersDiscount(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	code := coupon(t, "MATCHES", "amount", 20000, 0, 0, 0, 0)

	c, err := s.FindCoupon(ctx, code, 500000, 0)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	number, err := placeWithCoupon(t, s, c, 0)
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

// TestASpentCouponIsRefusedAsItselfRatherThanAsAnUnknownFailure holds the
// sentinel the checkout handler branches on.
//
// redeemCoupon MAPPED this refusal by matching strings.Contains on err.Error().
// pgconn renders a PgError as severity + message + SQLSTATE, and the constraint
// name RAISE sets travels in PgError.ConstraintName — it is not in that string,
// so neither branch could ever be taken. Every over-limit redemption fell
// through to the generic wrap, PlaceOrder's switch had no case for it, and the
// customer got a full-page 500 with their whole checkout form discarded.
//
// It is not a race. FindCoupon deliberately reads no limit at all, so a spent
// code passes the form validation every time and is refused here every time.
//
// TestTotalRedemptionLimitIsEnforced was already driving this exact path four
// times against a cap of two and asserting only `placed != 2` — so the dead
// branch executed green on every integration run. A count says the database held
// the line; only the ERROR says what the customer is about to be shown.
func TestASpentCouponIsRefusedAsItselfRatherThanAsAnUnknownFailure(t *testing.T) {
	s := cart.NewStore(pool)
	code := coupon(t, "SPENTONCE", "amount", 10000, 0, 0, 0, 1)

	c, err := s.FindCoupon(t.Context(), code, 500000, 0)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if _, placeErr := placeWithCoupon(t, s, c, 1); placeErr != nil {
		t.Fatalf("the first order should have gone through: %v", placeErr)
	}

	// The pre-check passes again — that is the point. The limit lives under
	// redeem_coupon's lock and FindCoupon does not read it.
	again, err := s.FindCoupon(t.Context(), code, 500000, 0)
	if err != nil {
		t.Fatalf("a spent coupon must still pass the field validation, "+
			"or this test is not exercising the transactional refusal: %v", err)
	}

	_, err = placeWithCoupon(t, s, again, 2)
	if !errors.Is(err, cart.ErrCouponUsedUp) {
		t.Fatalf("redeeming a spent coupon = %v, want ErrCouponUsedUp — the "+
			"checkout handler branches on this sentinel, and anything else is a "+
			"500 with the customer's address discarded", err)
	}
}

// placeWithCoupon places an order carrying a coupon and returns its number.
//
// Its own variant per call, not the seeded koto-over-ear one. Placing an order
// CONSUMES stock, and several tests here place four apiece, so every caller was
// drawing down one shared seeded row: the suite passed in file order and failed
// under -shuffle with "no true variant", which names an empty shelf and not the
// coupon under test. CLAUDE.md #23, found by adding the fifth consumer.
func placeWithCoupon(t *testing.T, s *cart.Store, c *cart.Coupon, n int) (string, error) {
	t.Helper()
	ctx := t.Context()

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	vid := freshVariant(t, "coupon-order")
	id := newCart(t, s)
	if err := s.Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	addr := &cart.Address{
		Email: "cp@example.com", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "路 1 號",
	}
	return s.PlaceOrder(ctx, id, uuid.NullUUID{}, shipID, addr, nil, c,
		"coupon-test-"+c.Code+"-"+strconv.Itoa(n))
}

// TestMoneyCeilingStaysInsideExactIntegerArithmetic is the alarm on a margin
// the comments elsewhere rely on.
//
// The percentage discount is subtotal * basis_points / 10000 in int64. That is
// exact for any input, and it also happens to agree with float64 arithmetic
// everywhere goen can reach — because the largest product the schema allows,
// 10^10 cents times 10^4 basis points, is 10^14, and float64 represents every
// integer up to 2^53 ≈ 9.0e15 exactly.
//
// This test is the alarm on that margin. Raising the money ceiling past ~9e11
// cents would put the product outside float64's exact range, at which point
// "the two agree" stops being true and any float creeping into a money path
// starts rounding differently on different totals. The integer code would still
// be right; the reasoning in the comments would not be.
func TestMoneyCeilingStaysInsideExactIntegerArithmetic(t *testing.T) {
	// The ceiling every money CHECK in the schema uses, read from the schema
	// rather than restated — a literal here would pass after somebody raised it.
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

// TestTheCouponWindowUsesOneClock proves the window is judged by the clock that
// set it.
//
// starts_at defaults to the DATABASE's now(). Comparing it against Go's
// time.Now() is comparing two clocks, and a container milliseconds ahead of its
// host makes a coupon created a moment ago read as "not started yet" — which is
// exactly how this surfaced.
//
// Both bounds are asserted, so the check cannot be satisfied by ignoring the
// window entirely.
// TestCancellingAnOrderGivesItsCouponSlotBack proves a cancelled checkout stops
// consuming a coupon's limits.
//
// The counts had no predicate on the order at all, and coupon_redemptions is
// append-only with INSERT, UPDATE and DELETE revoked from every role — so a
// checkout cancelled two minutes later spent a total-limit slot and a
// per-customer slot FOREVER. No door in the product could free either, and the
// back office could only switch the coupon off: max_redemptions is write-once.
//
// The row is kept, because it is the record of what an order was charged. It is
// the QUESTION that was wrong — the committed_orders lesson, one predicate
// answering two things, with the shop's own cancel unable to undo what it caused.
//
// A PENDING unpaid order still counts, and that half matters as much: not
// counting it is how two customers both pass the last remaining slot.
func TestCancellingAnOrderGivesItsCouponSlotBack(t *testing.T) {
	ctx := t.Context()

	var couponID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO coupons (code, description, kind, amount_cents, max_redemptions, per_customer_limit)
		VALUES ('ONESHOT', '只能用一次', 'amount', 20000, 1, 1)
		RETURNING id`).Scan(&couponID); err != nil {
		t.Fatalf("create coupon: %v", err)
	}

	// The header and its lines go in ONE transaction: orders_has_lines is
	// deferred, so an order committed on its own is refused at commit.
	place := func(t *testing.T) uuid.UUID {
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
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
		return orderID
	}

	first := place(t)
	if _, err := pool.Exec(ctx, `SELECT redeem_coupon($1, $2, NULL, 20000)`,
		couponID, first); err != nil {
		t.Fatalf("first redemption: %v", err)
	}

	// While it is still pending, the slot is taken — a second checkout must not
	// get it.
	second := place(t)
	if _, err := pool.Exec(ctx, `SELECT redeem_coupon($1, $2, NULL, 20000)`,
		couponID, second); err == nil {
		t.Fatal("a pending order's redemption did not hold the last slot — two " +
			"concurrent checkouts would both be allowed past it")
	}

	// Cancelling gives it back.
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now() WHERE id = $1`,
		first); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT redeem_coupon($1, $2, NULL, 20000)`,
		couponID, second); err != nil {
		t.Errorf("the slot is still consumed after the order was cancelled: %v — "+
			"coupon_redemptions is append-only and no role may delete one, so this "+
			"is the only door there is", err)
	}
}

func TestTheCouponWindowUsesOneClock(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	// Created this instant, with the schema's own default start.
	if _, err := pool.Exec(ctx, `
		INSERT INTO coupons (code, description, kind, amount_cents)
		VALUES ('JUSTNOW', '剛剛建立', 'amount', 20000)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.FindCoupon(ctx, "JUSTNOW", 500000, 0); err != nil {
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
	if _, err := s.FindCoupon(ctx, "FUTURE", 500000, 0); !errors.Is(err, cart.ErrCouponExpired) {
		t.Errorf("a coupon that has not started gave %v, want ErrCouponExpired", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO coupons (code, description, kind, amount_cents, starts_at, ends_at)
		VALUES ('LAPSED', '已經結束', 'amount', 20000,
		        now() - interval '2 days', now() - interval '1 day')`); err != nil {
		t.Fatalf("create lapsed: %v", err)
	}
	if _, err := s.FindCoupon(ctx, "LAPSED", 500000, 0); !errors.Is(err, cart.ErrCouponExpired) {
		t.Errorf("a lapsed coupon gave %v, want ErrCouponExpired", err)
	}
}
