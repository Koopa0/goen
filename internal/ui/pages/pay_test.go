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

// An unpaid order is 待付款 whether or not a payment can start; 完成付款 above it reads as already paid.
func TestThePayEyebrowNamesTheOrdersStatus(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		view PayView
		want i18n.Key
	}{
		{"open", PayView{Enabled: true}, i18n.KeyStatusAwaitingPayment},
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

// The hold is said in words on the blue while a payment can start: the deadline
// first, then when the hold ends. The deadline is said once, so the sentence over
// the pay button is gone. Nothing is drawn.
func TestThePayPageStatesTheDeadlineAndTheHoldInWords(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		locale                   i18n.Locale
		deadline, hold, placed   string
		resumedLead, resumedNote string
	}{
		{i18n.ZhHant, "請在 14:31 前開始付款", "商品保留到 15:02，逾時未付款，會自動取消訂單。時間以台灣時間為準。", "<dt>送出</dt>",
			"商品保留到 15:02", "逾時未付款，會自動取消訂單。時間以台灣時間為準。"},
		{i18n.En, "Start paying by 14:31", "Your items are reserved until 15:02 and the order is cancelled if it is still unpaid then. Times are Taiwan time.", "<dt>Placed</dt>",
			"Your items are reserved until 15:02", "The order is cancelled if it is still unpaid then. Times are Taiwan time."},
	} {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		view := PayView{
			Number: "GOEN-PAY", TotalCents: 149300, Enabled: true, Hold: payHold(false),
			Lines: []PayLine{{Name: "Nimbus", UnitCents: 149300, Quantity: 1}},
		}
		html := renderComponent(t, ctx, Pay(layouts.Page{Title: "Pay"}, view))
		for _, want := range []string{
			`<div class="goen-pay__window"><p class="goen-pay__deadline">` + tt.deadline + `</p><p class="goen-pay__hold">` + tt.hold + `</p></div>`,
			tt.placed, `<time datetime="2026-10-09T14:02">14:02</time>`,
			"<dt>" + i18n.T(ctx, i18n.KeyPayFactAmountDue) + "</dt>", `<small class="ui-statline__pre">NT$</small>1,493`,
		} {
			if !strings.Contains(html, want) {
				t.Errorf("%s: the pay page lacks %q", tt.locale, want)
			}
		}
		if n := strings.Count(html, tt.deadline); n != 1 {
			t.Errorf("%s: the deadline is said %d times, want once", tt.locale, n)
		}
		if strings.Contains(html, "ui-period") {
			t.Errorf("%s: the pay page draws the hold; it says it in words", tt.locale)
		}
		if !strings.Contains(html, `<dl class="ui-statline ui-statline--s">`) {
			t.Errorf("%s: the facts under the hold's panel are not the small line", tt.locale)
		}

		view.Hold.StartBy = time.Time{}
		html = renderComponent(t, ctx, Pay(layouts.Page{Title: "Pay"}, view))
		if want := `<p class="goen-pay__deadline">` + tt.resumedLead + `</p><p class="goen-pay__hold">` + tt.resumedNote + `</p>`; !strings.Contains(html, want) {
			t.Errorf("%s: a resumed session does not lead with the hold's end: want %q", tt.locale, want)
		}
		if strings.Contains(html, "14:31") {
			t.Errorf("%s: a resumed session still names a deadline to start paying", tt.locale)
		}
		if !strings.Contains(html, tt.placed) {
			t.Errorf("%s: a resumed session no longer says when the order was placed", tt.locale)
		}
	}
}

// Blue means a payment can still start; once none can, the hold's words sit on
// the neutral ground and name no deadline to start paying.
func TestThePayWindowIsBlueOnlyWhileAPaymentCanStart(t *testing.T) {
	t.Parallel()

	held := payHold(false)
	held.StartBy = time.Time{}
	for _, tt := range []struct {
		name   string
		view   PayView
		closed bool
	}{
		{"open", PayView{Enabled: true, Hold: payHold(false)}, false},
		{"window closed", PayView{Enabled: true, Closure: PayWindowClosed, Hold: held}, true},
		{"payments off", PayView{Hold: payHold(false)}, true},
	} {
		tt.view.Number = "GOEN-PAY"
		html := renderToString(t, Pay(layouts.Page{Title: "Pay"}, tt.view))
		if !strings.Contains(html, "goen-pay__window") {
			t.Errorf("%s: the pay page does not state the hold", tt.name)
		}
		if got := strings.Contains(html, "goen-pay__window--closed"); got != tt.closed {
			t.Errorf("%s: the hold on the neutral ground = %v, want %v", tt.name, got, tt.closed)
		}
		if got := strings.Contains(html, "請在"); got != !tt.closed {
			t.Errorf("%s: a deadline to start paying is named = %v, want %v", tt.name, got, !tt.closed)
		}
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
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the lapsed pay page lacks %q", want)
		}
	}
	for _, unwanted := range []string{"開始付款", "商品保留到", "前往付款", "goen-pay__window", "ui-period"} {
		if strings.Contains(html, unwanted) {
			t.Errorf("the lapsed pay page still shows %q", unwanted)
		}
	}
	if !strings.Contains(html, `<dl class="ui-statline">`) {
		t.Error("the lapsed pay page, with no panel above them, does not lead with the plain facts line")
	}
}

func TestAPayPageWithNoHoldStatesNone(t *testing.T) {
	t.Parallel()

	html := renderToString(t, Pay(layouts.Page{Title: "Pay"}, PayView{Number: "GOEN-PAY", Enabled: true}))
	for _, unwanted := range []string{"goen-pay__window", "ui-statline", "ui-period"} {
		if strings.Contains(html, unwanted) {
			t.Errorf("a pay page with no stored hold draws %s", unwanted)
		}
	}
}
