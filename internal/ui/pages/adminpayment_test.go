package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestTheOrderPageShowsItsPaymentAndEveryRefund(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := AdminOrderView{
		Number:  "GO-260930-000013",
		Payment: AdminPayment{Method: "信用卡(Stripe)", Card: "Visa •••• 4242", Captured: "NT$3,070", PaidAt: "2026-09-30 10:15"},
		Refunds: []AdminRefund{
			{Channel: "退回信用卡", Amount: "NT$1,200", At: "2026-10-01 09:00", Reason: "顧客改變心意", Staff: "審查管理員"},
			{Channel: "退回商店額度", Amount: "NT$1,870", At: "2026-10-01 09:05", Credit: true},
		},
	}
	html := renderComponent(t, ctx, AdminOrder(layouts.Page{}, &view))

	for _, want := range []string{
		i18n.T(ctx, i18n.KeyAdminPayTitle), "Visa •••• 4242", "NT$3,070", "2026-09-30 10:15",
		"NT$1,200", "顧客改變心意", "審查管理員", "NT$1,870", "退回商店額度",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the payment section is missing %q", want)
		}
	}
}
