package admin

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/coupon"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestCouponListMetersOnlyTheCouponsWithACap(t *testing.T) {
	t.Parallel()

	html := renderToString(t, Coupons(layouts.Page{Title: "Coupons"}, CouponsView{Rows: []Coupon{
		{Code: "HALF", Kind: coupon.Amount, MaxRedeem: 20, PerCustomer: 1, Redeemed: 5, Active: true, Current: true},
		{Code: "GONE", Kind: coupon.Amount, MaxRedeem: 2, PerCustomer: 1, Redeemed: 2, Active: true, Current: true},
		{Code: "OPEN", Kind: coupon.Amount, PerCustomer: 1, Redeemed: 9, Active: true, Current: true},
	}}))

	if got := strings.Count(html, `class="goen-chartmeter__track"`); got != 2 {
		t.Fatalf("list draws %d meters, want 2 (the uncapped coupon keeps its count alone)", got)
	}
	if !strings.Contains(html, `width="25.00%"`) {
		t.Error("list does not fill 5 uses of 20 to a quarter")
	}
	if !strings.Contains(html, `width="100.00%"`) {
		t.Error("list does not fill a used-up coupon")
	}
	for _, label := range []string{"5 / 20", "2 / 2"} {
		if !strings.Contains(html, `<span class="goen-chartmeter__label">`+label+`</span>`) {
			t.Errorf("list omits the count %q beside its meter", label)
		}
	}
	if strings.Contains(html, "9 / 0") {
		t.Error("list meters a coupon with no cap")
	}
}
