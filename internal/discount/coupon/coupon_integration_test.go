//go:build integration

package coupon_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/discount/coupon"
)

// insert stores c's terms as a coupons row and returns the row's id, so the
// schema's copy of the rule is asked about exactly the terms Apply was given.
func insert(t *testing.T, pool *pgxpool.Pool, c coupon.Coupon) uuid.UUID {
	t.Helper()
	var amount, percent, cap_ any
	if c.AmountCents > 0 {
		amount = c.AmountCents
	}
	if c.PercentBP > 0 {
		percent = c.PercentBP
	}
	if c.CapCents > 0 {
		cap_ = c.CapCents
	}
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO coupons (code, description, kind, amount_cents, percent_bp,
		                     max_discount_cents, min_subtotal_cents)
		VALUES ($1, '測試折扣', $2, $3, $4, $5, $6) RETURNING id`,
		c.Code, string(c.Kind), amount, percent, cap_, c.MinimumCents).Scan(&id); err != nil {
		t.Fatalf("create coupon %s: %v", c.Code, err)
	}
	return id
}

// TestCouponPricing is the arithmetic, every case a hand-computed literal. The
// schema restates it as coupon_discount, which orders_have_lines refuses an
// order by, so each case is asked of both: two copies that drift apart refuse
// the orders checkout prices.
func TestCouponPricing(t *testing.T) {
	pool := dbtest.Pool(t)

	flat200 := coupon.Coupon{Code: "FLAT200", Kind: coupon.Amount, AmountCents: 20000}
	pct20 := coupon.Coupon{Code: "PCT20", Kind: coupon.Percent, PercentBP: 2000}
	pct20Cap := coupon.Coupon{Code: "PCT20CAP", Kind: coupon.Percent, PercentBP: 2000, CapCents: 50000}
	ship := coupon.Coupon{Code: "SHIP", Kind: coupon.FreeShipping}
	big := coupon.Coupon{Code: "BIG", Kind: coupon.Amount, AmountCents: 900000}
	pct15Cap := coupon.Coupon{Code: "PCT15CAP", Kind: coupon.Percent, PercentBP: 1500, CapCents: 14950}
	ids := map[string]uuid.UUID{}
	for _, c := range []coupon.Coupon{flat200, pct20, pct20Cap, ship, big, pct15Cap} {
		ids[c.Code] = insert(t, pool, c)
	}

	tests := []struct {
		name             string
		coupon           coupon.Coupon
		subtotal         int64
		wantDiscount     int64
		wantFreeShipping bool
	}{
		{"flat amount", flat200, 500000, 20000, false},
		{"twenty percent", pct20, 500000, 100000, false},
		{"capped percent", pct20Cap, 500000, 50000, false},
		{"percent under the cap", pct20Cap, 100000, 20000, false},
		{"free shipping is not a discount", ship, 500000, 0, true},
		// The discount may never exceed the subtotal, or the total goes negative
		// and orders_total_non_negative refuses the checkout.
		{"coupon larger than the order", big, 100000, 100000, false},
		// 20% of 1001 cents is 200.2 cents; integer division gives 200, already
		// a whole NT$.
		{"sub-cent remainder truncates", pct20, 1001, 200, false},
		// 20% of 5005 cents is 1001, which rounds up to the next whole NT$.
		{"discount rounds up to a whole NT$", pct20, 5005, 1100, false},
		// 15% of 99900 is 14985, rounded up to 15000; the cap of 14950 is
		// floored to 14900, and binds after the rounding.
		{"the cap binds after rounding", pct15Cap, 99900, 14900, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			discount, freeShipping, err := tt.coupon.Apply(tt.subtotal, false)
			if err != nil {
				t.Fatalf("apply %s: %v", tt.coupon.Code, err)
			}
			if discount != tt.wantDiscount {
				t.Errorf("discount on %d is %d, want %d", tt.subtotal, discount, tt.wantDiscount)
			}
			if freeShipping != tt.wantFreeShipping {
				t.Errorf("free shipping is %v, want %v", freeShipping, tt.wantFreeShipping)
			}
			var schema pgtype.Int8
			if err := pool.QueryRow(t.Context(), `SELECT coupon_discount($1, $2)`,
				ids[tt.coupon.Code], tt.subtotal).Scan(&schema); err != nil {
				t.Fatalf("coupon_discount %s: %v", tt.coupon.Code, err)
			}
			if !schema.Valid || schema.Int64 != tt.wantDiscount {
				t.Errorf("coupon_discount on %d is %+v, want %d", tt.subtotal, schema, tt.wantDiscount)
			}
		})
	}
}

// TestCouponMinimumSpend proves the threshold binds at its own boundary.
func TestCouponMinimumSpend(t *testing.T) {
	pool := dbtest.Pool(t)
	c := coupon.Coupon{Code: "MIN1000", Kind: coupon.Amount, AmountCents: 20000, MinimumCents: 100000}
	id := insert(t, pool, c)

	if _, _, err := c.Apply(99999, false); !errors.Is(err, coupon.ErrBelowMinimum) {
		t.Errorf("below the minimum gave %v, want coupon.ErrBelowMinimum", err)
	}
	// Exactly the minimum qualifies — the boundary.
	if _, _, err := c.Apply(100000, false); err != nil {
		t.Errorf("exactly the minimum was refused: %v", err)
	}
	// The schema's copy says the same: nothing below, the amount at it.
	var below, at pgtype.Int8
	if err := pool.QueryRow(t.Context(), `
		SELECT coupon_discount($1, 99999), coupon_discount($1, 100000)`, id).Scan(&below, &at); err != nil {
		t.Fatalf("coupon_discount at the minimum: %v", err)
	}
	if below.Valid || !at.Valid || at.Int64 != 20000 {
		t.Errorf("coupon_discount is %+v below the minimum and %+v at it, want NULL and 20000", below, at)
	}
}
