//go:build integration

package checkout_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/checkout/checkouttest"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// TestStore_PlaceOrderPricingRules: PlaceOrder, run as the store role, commits
// every shape of order checkout prices: orders_have_lines prices the order
// again at COMMIT, and the shipping, discount and amount owed it commits with
// are the ones the quote gave. orders_have_lines prices an order again at
// COMMIT, from the rules the checkout quote follows, for every role but the
// owner. So each shape of quote is placed here AS STORE, the role checkout runs
// as, and must commit with the money the quote gave. The rates are the
// development catalogue's, read by version id so a version another test
// publishes cannot move them: home delivery NT$80, pickup NT$60, both free from
// NT$3,000, and NT$200 more to 離島 (890) by home delivery only. Each unit is
// NT$1,999.
func TestStore_PlaceOrderPricingRules(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(storeRolePool(t))
	const (
		home   = "ffff0002-0000-4000-8000-000000000001"
		pickup = "ffff0002-0000-4000-8000-000000000002"
	)

	suffix := strings.ToUpper(uuid.NewString()[:6])
	code := func(name, kind string, amount, percent, cap_, minSpend any) string {
		t.Helper()
		c := name + suffix
		if _, err := pool.Exec(ctx, `
			INSERT INTO coupons (code, description, kind, amount_cents, percent_bp,
			                     max_discount_cents, min_subtotal_cents)
			VALUES ($1, '報價規則', $2, $3, $4, $5, $6)`,
			c, kind, amount, percent, cap_, minSpend); err != nil {
			t.Fatalf("create coupon %s: %v", c, err)
		}
		return c
	}
	amount := code("AMT", "amount", 20000, nil, nil, 0)
	pastTheGoods := code("ALL", "amount", 900000, nil, nil, 0)
	// 15% of NT$1,999 is NT$299.85, a whole NT$300 rounded up, then the
	// NT$149.50 cap floored to NT$149: the cap binds after rounding.
	capped := code("CAP", "percent", nil, 1500, 14950, 0)
	// 10% of NT$1,999 is NT$199.90, rounded up to NT$200, at exactly the
	// minimum spend.
	atMinimum := code("MIN", "percent", nil, 1000, nil, 199900)
	freeDelivery := code("SHIP", "free_shipping", nil, nil, nil, 0)

	mainland := delivery.Details{
		Email: "quote@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	offshore := mainland
	offshore.PostalCode, offshore.City, offshore.District = "890", "金門縣", "金城鎮"
	store := delivery.Details{
		Email: "quote@example.com", RecipientName: "王小明", Phone: "0912345678",
		PickupChain: "seven_eleven",
	}

	for _, tc := range []struct {
		name         string
		version      string
		to           delivery.Details
		units        int32
		coupon       string
		wantShipping int64
		wantDiscount int64
	}{
		{"home delivery under the threshold", home, mainland, 1, "", 8000, 0},
		{"home delivery to an outlying island", home, offshore, 1, "", 28000, 0},
		{"home delivery at the threshold", home, mainland, 2, "", 0, 0},
		{"the threshold leaves the crossing", home, offshore, 2, "", 20000, 0},
		{"pickup is in no zone", pickup, store, 1, "", 6000, 0},
		{"a fixed amount", home, mainland, 1, amount, 8000, 20000},
		{"a fixed amount past the goods owes nothing", home, mainland, 2, pastTheGoods, 0, 399800},
		{"a percentage capped after rounding", home, mainland, 1, capped, 8000, 14900},
		{"a percentage at its minimum spend", home, mainland, 1, atMinimum, 8000, 20000},
		{"a free-delivery code", home, mainland, 1, freeDelivery, 0, 0},
		{"a free-delivery code leaves the crossing", home, offshore, 1, freeDelivery, 20000, 0},
		{"a free-delivery code to a pickup counter", pickup, store, 1, freeDelivery, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := checkouttest.NewCart(t, s)
			if err := s.Carts().Add(ctx, id, ordertest.FreshVariant(t, pool, "quote-rule"), tc.units); err != nil {
				t.Fatalf("add: %v", err)
			}
			to := tc.to
			number, err := checkouttest.PlaceOrder(t, s, ctx, id, uuid.NullUUID{},
				uuid.MustParse(tc.version), &to, tc.coupon, "quote-rule-"+uuid.NewString())
			if err != nil {
				t.Fatalf("the storefront could not place an order checkout priced: %v", err)
			}
			var shipping, discount, owed int64
			if err := pool.QueryRow(ctx, `
				SELECT shipping_cents, discount_cents, order_amount_after_credit(id)
				FROM orders WHERE order_number = $1`, number).
				Scan(&shipping, &discount, &owed); err != nil {
				t.Fatalf("read %s: %v", number, err)
			}
			if shipping != tc.wantShipping || discount != tc.wantDiscount {
				t.Errorf("delivery %d and discount %d, want %d and %d",
					shipping, discount, tc.wantShipping, tc.wantDiscount)
			}
			if want := int64(tc.units)*199900 - tc.wantDiscount + tc.wantShipping; owed != want {
				t.Errorf("owes %d, want %d", owed, want)
			}
		})
	}
}
