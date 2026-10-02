package admin

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestTheOrderPageShowsItsPaymentAndEveryRefund(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := OrderView{
		Number:  "GO-260930-000013",
		Payment: Payment{Method: "信用卡(Stripe)", Card: "Visa •••• 4242", Captured: "NT$3,070", PaidAt: "2026-09-30 10:15"},
		Refunds: []Refund{
			{Channel: "退回信用卡", Amount: "NT$1,200", At: "2026-10-01 09:00", Reason: "顧客改變心意", Staff: "審查管理員"},
			{Channel: "退回商店額度", Amount: "NT$1,870", At: "2026-10-01 09:05"},
		},
	}
	html := renderComponent(t, ctx, Order(layouts.Page{}, &view))

	for _, want := range []string{
		i18n.T(ctx, i18n.KeyAdminPayTitle), "Visa •••• 4242", "NT$3,070", "2026-09-30 10:15",
		"NT$1,200", "顧客改變心意", "審查管理員", "NT$1,870", "退回商店額度",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the payment section is missing %q", want)
		}
	}
}

func TestAnOrderPaidWholeWithCreditSaysSoAndNeverNoPayment(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		view := OrderView{
			Number: "GO-260930-000014", Status: pages.FulfillmentPending, Funded: true,
			SubtotalCents: 50000, ShippingCents: 6000, CreditCents: 56000,
			Payment: Payment{Method: i18n.T(ctx, i18n.KeyAdminPayMethodCredit)},
			Next:    []Transition{{Value: pages.FulfillmentPicking, Label: "picking"}},
		}
		html := renderComponent(t, ctx, Order(layouts.Page{}, &view))
		for name, want := range map[string]string{
			"the payment method":  i18n.T(ctx, i18n.KeyAdminPayMethodCredit),
			"the credit line":     i18n.T(ctx, i18n.KeyOrderCreditApplied),
			"the credit figure":   "-NT$560",
			"the amount due line": i18n.T(ctx, i18n.KeyOrderAmountDue),
		} {
			if !strings.Contains(html, want) {
				t.Errorf("%s: missing %s (%q)", locale, name, want)
			}
		}
		if strings.Contains(html, i18n.T(ctx, i18n.KeyAdminPayNone)) {
			t.Errorf("%s: a funded order says no payment was received", locale)
		}
		if strings.Contains(html, `value="cancelled"`) {
			t.Errorf("%s: a funded order offers a plain cancellation", locale)
		}
	}

	t.Run("an order credit did not pay shows no credit line", func(t *testing.T) {
		t.Parallel()
		ctx := i18n.WithLocale(t.Context(), i18n.En)
		html := renderComponent(t, ctx, Order(layouts.Page{}, &OrderView{Number: "GO-1", SubtotalCents: 50000, OwedCents: 50000}))
		if strings.Contains(html, i18n.T(ctx, i18n.KeyOrderCreditApplied)) {
			t.Error("an order with no credit shows a credit line")
		}
	})
}
