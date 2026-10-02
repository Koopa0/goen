package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// A NT$100 coupon against NT$80 delivery is two rows, as on the order page,
// never one net 「已折抵 -NT$20」 that hides both.
func TestThePayPageListsDeliveryAndTheDiscountApart(t *testing.T) {
	t.Parallel()

	view := PayView{
		Number:     "GOEN-PAY",
		TotalCents: 98000,
		Lines: []PayLine{
			{Name: "Nimbus", UnitCents: 100000, Quantity: 1},
		},
		Enabled:        true,
		ShippingName:   "宅配到府",
		ShippingCents:  8000,
		DiscountCents:  10000,
		DiscountReason: "ACCEPT100 · 驗收折扣",
	}
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Pay(layouts.Page{Title: "Pay"}, view))
	for _, want := range []string{
		"<dt>運費（宅配到府）</dt>", ">NT$80<",
		"<dt>折扣（ACCEPT100 · 驗收折扣）</dt>", ">-NT$100<",
		">NT$980<",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the pay page lacks %q", want)
		}
	}
	if strings.Contains(html, "-NT$20<") {
		t.Error("the pay page nets the coupon against delivery")
	}
	if strings.Contains(html, i18n.T(ctx, i18n.KeyShippingAndTax)) {
		t.Error("the pay page uses the Stripe line item's label")
	}
}

func TestThePayPageNamesStoreCreditAndFreeDelivery(t *testing.T) {
	t.Parallel()

	view := PayView{
		Number:     "GOEN-PAY",
		TotalCents: 80000,
		Lines: []PayLine{
			{Name: "Nimbus", UnitCents: 100000, Quantity: 1},
		},
		ShippingName: "超商取貨",
		CreditCents:  20000,
	}
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Pay(layouts.Page{Title: "Pay"}, view))
	for _, want := range []string{
		"<dt>" + i18n.T(ctx, i18n.KeyOrderCreditApplied) + "</dt>", ">-NT$200<",
		">" + i18n.T(ctx, i18n.KeyFreeShipping) + "<", ">NT$800<",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the pay page lacks %q", want)
		}
	}
	if strings.Contains(html, "<dt>"+i18n.T(ctx, i18n.KeyDiscount)) {
		t.Error("an order with no discount grew a discount row")
	}
}

// 完成付款 is an instruction; where no payment can start it is not true.
func TestThePayEyebrowSaysWhatTheShopperCanDo(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		view PayView
		want i18n.Key
	}{
		{"open", PayView{Enabled: true}, i18n.KeyPayEyebrow},
		{"payments off", PayView{}, i18n.KeyStatusAwaitingPayment},
		{"window closed", PayView{Enabled: true, Closure: PayWindowClosed}, i18n.KeyStatusAwaitingPayment},
		{"cancelled", PayView{Enabled: true, Closure: PayOrderCancelled}, i18n.KeyStatusCancelled},
	} {
		if got := tt.view.EyebrowKey(); got != tt.want {
			t.Errorf("%s: eyebrow is %s, want %s", tt.name, got, tt.want)
		}
	}

	off := renderToString(t, Pay(layouts.Page{Title: "Pay"}, PayView{Number: "GOEN-PAY"}))
	if !strings.Contains(off, `<p class="goen-pagehead__eyebrow">待付款</p>`) {
		t.Error("with payments off the pay page's eyebrow does not say 待付款")
	}
}
