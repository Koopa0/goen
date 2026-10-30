package coupon_test

import (
	"testing"

	"github.com/koopa0/goen/internal/discount/coupon"
	"github.com/koopa0/goen/internal/i18n"
)

// TestRedeemed_Label: a placed order's discount row is named by the coupon's code. The
// coupon's name follows only where it says more than the code, and a free-delivery
// code says what it did, since it takes nothing off the goods.
func TestRedeemed_Label(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		redeemed coupon.Redeemed
		want     map[i18n.Locale]string
	}{
		{"no coupon", coupon.Redeemed{}, map[i18n.Locale]string{i18n.ZhHant: "折扣", i18n.En: "Discount"}},
		{"name differs", coupon.Redeemed{Code: "SAVE200", Description: "滿額折抵", Kind: coupon.Amount}, map[i18n.Locale]string{
			i18n.ZhHant: "折扣碼 SAVE200（滿額折抵）", i18n.En: "Discount code SAVE200 (滿額折抵)",
		}},
		{"name is the code", coupon.Redeemed{Code: "SAVE200", Description: "save200", Kind: coupon.Percent}, map[i18n.Locale]string{
			i18n.ZhHant: "折扣碼 SAVE200", i18n.En: "Discount code SAVE200",
		}},
		{"name begins with the code", coupon.Redeemed{Code: "SAVE200", Description: "SAVE200 測試折扣", Kind: coupon.Amount}, map[i18n.Locale]string{
			i18n.ZhHant: "折扣碼 SAVE200", i18n.En: "Discount code SAVE200",
		}},
		{"no name", coupon.Redeemed{Code: "SAVE200", Kind: coupon.Amount}, map[i18n.Locale]string{
			i18n.ZhHant: "折扣碼 SAVE200", i18n.En: "Discount code SAVE200",
		}},
		{"free delivery", coupon.Redeemed{Code: "FREESHIP", Description: "免運活動", Kind: coupon.FreeShipping}, map[i18n.Locale]string{
			i18n.ZhHant: "折扣碼 FREESHIP：免運", i18n.En: "Discount code FREESHIP: free delivery",
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for locale, want := range tt.want {
				if got := tt.redeemed.Label(i18n.WithLocale(t.Context(), locale)); got != want {
					t.Errorf("%s: Label() = %q, want %q", locale, got, want)
				}
			}
		})
	}
}

// TestRedeemed_Used: an order used a coupon when it has a code, whatever the coupon took
// off: a free-delivery code leaves the discount at nothing and is still listed.
func TestRedeemed_Used(t *testing.T) {
	t.Parallel()
	if (coupon.Redeemed{}).Used() {
		t.Error("Redeemed{}.Used() = true, want false")
	}
	if !(coupon.Redeemed{Code: "FREESHIP", Kind: coupon.FreeShipping}).Used() {
		t.Error("a free-delivery code Used() = false, want true")
	}
}
