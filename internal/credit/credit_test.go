package credit

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestReason(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		kind   Kind
		reason string
		want   string
	}{
		{i18n.En, OrderReversal, "", "Order cancelled, credit returned"},
		{i18n.En, OrderSpend, "", "Applied to an order"},
		{i18n.En, ReturnPayout, "", "Credit for a return"},
		{i18n.En, PointsRedemption, "", "Points redeemed"},
		{i18n.ZhHant, OrderSpend, "", "訂單折抵"},
		{i18n.ZhHant, OrderReversal, "", "訂單取消，購物金退回"},
		{i18n.ZhHant, ReturnPayout, "", "退貨退回購物金"},
		{i18n.ZhHant, PointsRedemption, "", "點數兌換"},
		{i18n.En, StaffGrant, "goodwill for a late parcel", "goodwill for a late parcel"},
		// Staff text that spells a label shows as typed: kind, not the words, decides.
		{i18n.En, StaffGrant, "points", "points"},
		{i18n.ZhHant, StaffGrant, "退貨退回購物金", "退貨退回購物金"},
		// And a system posting never shows its (absent or misleading) reason.
		{i18n.En, PointsRedemption, "goodwill", "Points redeemed"},
	} {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		if got := Reason(ctx, tt.kind, tt.reason); got != tt.want {
			t.Errorf("%s Reason(%q, %q) = %q, want %q", tt.locale, tt.kind, tt.reason, got, tt.want)
		}
	}
}
