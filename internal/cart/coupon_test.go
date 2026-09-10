package cart

import (
	"math"
	"testing"
)

func TestPercentCouponDoesNotOverflowAnInRangeSubtotal(t *testing.T) {
	t.Parallel()

	coupon := Coupon{kind: "percent", percentBP: 10000}
	discount, freeShipping, err := coupon.Apply(math.MaxInt64)
	if err != nil {
		t.Fatalf("Apply(MaxInt64): %v", err)
	}
	if discount != math.MaxInt64 {
		t.Errorf("discount = %d, want %d", discount, int64(math.MaxInt64))
	}
	if freeShipping {
		t.Error("a percent coupon reported free shipping")
	}
}
