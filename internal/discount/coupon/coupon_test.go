package coupon

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want Kind
		ok   bool
	}{
		{"amount", Amount, true},
		{"percent", Percent, true},
		{"free_shipping", FreeShipping, true},
		{"", "", false},
		{"Percent", "", false},
		{"bogo", "", false},
	}
	for _, tt := range tests {
		got, ok := Parse(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("Parse(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

var kindsAdmitted = regexp.MustCompile(`coupons_kind_known CHECK \(kind IN \(([^)]*)\)\)`)

func TestEveryAdmittedKindHasAConstant(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "001_initial_schema.up.sql"))
	if err != nil {
		t.Fatalf("read the schema: %v", err)
	}
	m := kindsAdmitted.FindSubmatch(schema)
	if m == nil {
		t.Fatal("coupons_kind_known not found in the schema")
	}
	for _, lit := range regexp.MustCompile(`'([a-z_]+)'`).FindAllSubmatch(m[1], -1) {
		if _, ok := Parse(string(lit[1])); !ok {
			t.Errorf("the schema admits kind %q and Parse refuses it", lit[1])
		}
	}
}

func TestPercentCouponDoesNotOverflowAnInRangeSubtotal(t *testing.T) {
	t.Parallel()

	coupon := Coupon{Kind: Percent, PercentBP: 10000}
	discount, freeShipping, err := coupon.Apply(math.MaxInt64, false)
	if err != nil {
		t.Fatalf("Apply(MaxInt64): %v", err)
	}
	// A whole NT$ below the subtotal: the round-up must not overflow.
	if want := int64(math.MaxInt64) / centsPerYuan * centsPerYuan; discount != want {
		t.Errorf("discount = %d, want %d", discount, want)
	}
	if freeShipping {
		t.Error("a percent coupon reported free shipping")
	}
}

// A percentage discount is a whole NT$, rounded up so the shopper never pays
// more than the percentage allows.
func TestPercentCouponDiscountIsAWholeYuanRoundedInTheCustomersFavour(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		coupon   Coupon
		subtotal int64
		want     int64
	}{
		// 15% of NT$999 is NT$149.85; NT$150 off leaves NT$849, not NT$849.15.
		{"the issue's example", Coupon{Kind: Percent, PercentBP: 1500}, 99900, 15000},
		{"already whole", Coupon{Kind: Percent, PercentBP: 2000}, 500000, 100000},
		{"a cent over a whole yuan", Coupon{Kind: Percent, PercentBP: 1000}, 100110, 10100},
		{"a sub-cent remainder is dropped before rounding", Coupon{Kind: Percent, PercentBP: 2000}, 1001, 200},
		{"half a yuan of discount rounds to one", Coupon{Kind: Percent, PercentBP: 500}, 1000, 100},
		{"the cap binds after rounding", Coupon{Kind: Percent, PercentBP: 1500, CapCents: 14950}, 99900, 14900},
		{"a whole cap is reached exactly", Coupon{Kind: Percent, PercentBP: 5000, CapCents: 20000}, 999900, 20000},
		{"never past a fractional subtotal", Coupon{Kind: Percent, PercentBP: 10000}, 100050, 100000},
		{"nothing to discount", Coupon{Kind: Percent, PercentBP: 1500}, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, _, err := tc.coupon.Apply(tc.subtotal, false)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("Apply(%d) = %d, want %d", tc.subtotal, got, tc.want)
			}
			if got%100 != 0 {
				t.Errorf("discount %d is not a whole NT$", got)
			}
		})
	}
}

func TestApplyRefusesAnUnknownKind(t *testing.T) {
	t.Parallel()

	_, _, err := Coupon{Code: "ODD", Kind: "bogo"}.Apply(1000, false)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Apply with kind bogo: err = %v, want ErrNotFound", err)
	}
}
