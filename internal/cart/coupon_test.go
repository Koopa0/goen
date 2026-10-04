package cart

import (
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/ratelimit"
)

func TestPercentCouponDoesNotOverflowAnInRangeSubtotal(t *testing.T) {
	t.Parallel()

	coupon := Coupon{kind: "percent", percentBP: 10000}
	discount, freeShipping, err := coupon.Apply(math.MaxInt64)
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
		{"the issue's example", Coupon{kind: "percent", percentBP: 1500}, 99900, 15000},
		{"already whole", Coupon{kind: "percent", percentBP: 2000}, 500000, 100000},
		{"a cent over a whole yuan", Coupon{kind: "percent", percentBP: 1000}, 100110, 10100},
		{"a sub-cent remainder is dropped before rounding", Coupon{kind: "percent", percentBP: 2000}, 1001, 200},
		{"half a yuan of discount rounds to one", Coupon{kind: "percent", percentBP: 500}, 1000, 100},
		{"the cap binds after rounding", Coupon{kind: "percent", percentBP: 1500, capCents: 14950}, 99900, 14900},
		{"a whole cap is reached exactly", Coupon{kind: "percent", percentBP: 5000, capCents: 20000}, 999900, 20000},
		{"never past a fractional subtotal", Coupon{kind: "percent", percentBP: 10000}, 100050, 100000},
		{"nothing to discount", Coupon{kind: "percent", percentBP: 1500}, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, _, err := tc.coupon.Apply(tc.subtotal)
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

// A wrong coupon code is charged to the client whatever cart it brings. An IPv6
// client chooses the low 64 bits of its address freely, so a client keyed on
// the whole address would buy a fresh allowance with every rotation.
func TestAWrongCouponIsChargedToAWholeIPv6Slash64(t *testing.T) {
	t.Parallel()
	h := NewHandler(&Store{}, slog.New(slog.DiscardHandler), true, ratelimit.New(ratelimit.Config{
		Every: time.Hour, Burst: 1, TTL: time.Hour, MaxKeys: 8,
	}), nil, nil)
	// Each ask brings a new cart, so only the client's own key can be spent.
	keys := func(remoteAddr string) [2]string {
		t.Helper()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout", http.NoBody)
		req.RemoteAddr = remoteAddr
		return couponKeys(req, uuid.New())
	}
	spent := func(remoteAddr string) bool {
		t.Helper()
		for _, key := range keys(remoteAddr) {
			reservation, _, ok := h.couponMisses.Reserve(key)
			if !ok {
				return true
			}
			reservation.Refund()
		}
		return false
	}

	for range 100 {
		for _, key := range keys("[2001:db8:1:2::1]:1000") {
			if reservation, _, ok := h.couponMisses.Reserve(key); ok {
				reservation.Keep()
			}
		}
	}
	if !spent("[2001:db8:1:2::1]:1000") {
		t.Fatal("a client that missed a hundred times still has codes to try")
	}
	if !spent("[2001:db8:1:2:aaaa:bbbb:cccc:dddd]:1001") {
		t.Error("another address in the same /64 has codes to try; rotating the low bits " +
			"bought a fresh allowance")
	}
	if spent("[2001:db8:1:3::1]:1002") {
		t.Error("an address in another /64 has no codes to try; it shared a bucket")
	}
}

func TestApplyRefusesAnUnknownKind(t *testing.T) {
	t.Parallel()

	_, _, err := Coupon{code: "ODD", kind: "bogo"}.Apply(1000)
	if !errors.Is(err, ErrNoSuchCoupon) {
		t.Fatalf("Apply with kind bogo: err = %v, want ErrNoSuchCoupon", err)
	}
}
