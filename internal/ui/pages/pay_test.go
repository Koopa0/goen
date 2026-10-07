package pages

import (
	"strings"
	"testing"
	"time"

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

func payHold(lapsed bool) PayHold {
	cst := time.FixedZone("CST", 8*3600)
	placed := time.Date(2026, 10, 9, 14, 2, 0, 0, cst)
	h := PayHold{PlacedAt: placed, StartBy: placed.Add(29 * time.Minute), Until: placed.Add(time.Hour)}
	if lapsed {
		h.CancelledAt = placed.Add(61 * time.Minute)
	}
	return h
}

func TestThePayPageStatesTheDeadlineAndTheHold(t *testing.T) {
	t.Parallel()

	view := PayView{
		Number: "GOEN-PAY", TotalCents: 149300, Enabled: true, Hold: payHold(false),
		Lines: []PayLine{{Name: "Nimbus", UnitCents: 149300, Quantity: 1}},
	}
	html := renderToString(t, Pay(layouts.Page{Title: "Pay"}, view))
	for _, want := range []string{
		`ui-statline`, "<dt>送出</dt>", `<dd><time datetime="2026-10-09T14:02">14:02</time>`, "<dt>開始付款期限</dt>", `<dd><time datetime="2026-10-09T14:31">14:31</time>`, "台灣時間",
		"<dt>庫存保留至</dt>", `<dd><time datetime="2026-10-09T15:02">15:02</time>`,
		"<dt>應付金額</dt>", `<small class="ui-statline__pre">NT$</small>1,493`,
		`data-unit="minute"`, `data-mark`, `data-span="extra"`,
		"請在 14:31（台灣時間）前開始付款。",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the pay page lacks %q", want)
		}
	}
	if strings.Contains(html, `data-cell="`) {
		t.Error("the pay page marks a cell as now or past on an open hold")
	}

	view.Hold.StartBy = time.Time{}
	html = renderToString(t, Pay(layouts.Page{Title: "Pay"}, view))
	if strings.Contains(html, "開始付款期限") || strings.Contains(html, "data-mark") {
		t.Error("a resumed session still shows a start-paying deadline")
	}
	if !strings.Contains(html, "<dt>送出</dt>") {
		t.Error("a resumed session no longer says when the order was placed")
	}
}

func TestAPayPageWhoseHoldLapsedSaysNothingWasCharged(t *testing.T) {
	t.Parallel()

	view := PayView{
		Number: "GOEN-PAY", TotalCents: 149300, Closure: PayOrderCancelled, Hold: payHold(true),
		Lines: []PayLine{{Name: "Nimbus", UnitCents: 149300, Quantity: 1}},
	}
	html := renderToString(t, Pay(layouts.Page{Title: "Pay"}, view))
	for _, want := range []string{
		"<dt>送出</dt>", "<dt>自動取消</dt>", `<time datetime="2026-10-09T15:03">15:03</time>`, "庫存保留結束時仍未付款", "<dt>收取金額</dt>", `<small class="ui-statline__pre">NT$</small>0`,
		`data-cell="past"`, `data-mark`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the lapsed pay page lacks %q", want)
		}
	}
	for _, unwanted := range []string{"開始付款期限", "庫存保留至", "前往付款"} {
		if strings.Contains(html, unwanted) {
			t.Errorf("the lapsed pay page still shows %q", unwanted)
		}
	}
	if got := strings.Count(html, "<i data-cell=\"past\""); got != 60 {
		t.Errorf("%d past cells, want the whole grid of 60", got)
	}
}

func TestAPayPageWithNoHoldDrawsNoGrid(t *testing.T) {
	t.Parallel()

	html := renderToString(t, Pay(layouts.Page{Title: "Pay"}, PayView{Number: "GOEN-PAY", Enabled: true}))
	if strings.Contains(html, "ui-period") || strings.Contains(html, "ui-statline") {
		t.Error("a pay page with no stored hold drew facts or a grid")
	}
}
