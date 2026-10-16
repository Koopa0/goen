package buyer

import (
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/shipping"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

// TestCanCancelIsAboutFundingNotJustStatus holds that a captured order still at
// pending is not offered cancel, which CancelOrderByCustomer would refuse.
func TestCanCancelIsAboutFundingNotJustStatus(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name      string
		status    order.FulfillmentStatus
		committed bool
		want      bool
	}{
		{"unpaid and not started", "pending", false, true},
		{"paid but not yet picked", "pending", true, false},
		{"being picked", "picking", false, false},
		{"already cancelled", "cancelled", false, false},
		{"shipped", "shipped", true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := &OrderView{Status: tt.status, Committed: tt.committed}
			if got := v.CanCancel(); got != tt.want {
				t.Errorf("CanCancel() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestTheOrderPageShowsTheDiscountAndWhy asserts the HTML, because the view
// model can hold every number correctly while the template renders one of them
// nowhere.
func TestTheOrderPageShowsTheDiscountAndWhy(t *testing.T) {
	v := &OrderView{
		Number: "GO-260101-000001", Status: "pending",
		SubtotalCents: 100000, ShippingCents: 6000,
		DiscountCents: 20000, DiscountReason: "SAVE200 · 滿額折抵",
		ShippingName: "宅配",
	}

	html := renderToString(t, Order(layouts.Page{Title: "訂單"}, v))
	for _, want := range []string{"SAVE200", "滿額折抵", "-NT$200"} {
		if !strings.Contains(html, want) {
			t.Errorf("the order summary does not mention %q", want)
		}
	}

	plain := &OrderView{
		Number: "GO-260101-000002", Status: "pending",
		SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
	}
	if got := renderToString(t, Order(layouts.Page{Title: "訂單"}, plain)); strings.Contains(got, "-NT$0") {
		t.Error("an order with no discount renders a zero discount row")
	}
}

// TestACreditPaidOrdersCancelSaysItVoidsTheInvoice: the customer's own press is
// the consent to voiding the 統一發票, so the form says so in both languages,
// and only where an invoice was owed.
func TestACreditPaidOrdersCancelSaysItVoidsTheInvoice(t *testing.T) {
	t.Parallel()
	viewOf := func(creditCents, owedCents int64) *OrderView {
		return &OrderView{
			Number: "GO-260101-000003", Status: "pending", ShippingName: "宅配",
			SubtotalCents: 100000, ShippingCents: 6000,
			CreditCents: creditCents, OwedCents: owedCents,
		}
	}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		voids := templ.EscapeString(i18n.T(ctx, i18n.KeyOrderCancelVoidsInvoice))
		if got := renderIn(t, locale, Order(layouts.Page{Title: "order"}, viewOf(106000, 0))); !strings.Contains(got, voids) {
			t.Errorf("%s: the cancel form of a credit-paid order does not say the invoice is voided", locale)
		}
		// Unpaid, part paid by credit, and free: none was owed an invoice.
		for _, uninvoiced := range []*OrderView{viewOf(0, 106000), viewOf(6000, 100000), viewOf(0, 0)} {
			if got := renderIn(t, locale, Order(layouts.Page{Title: "order"}, uninvoiced)); strings.Contains(got, voids) {
				t.Errorf("%s: an order with %d credit owing %d says cancelling voids an invoice it never had",
					locale, uninvoiced.CreditCents, uninvoiced.OwedCents)
			}
		}
	}
}

// TestOnlyAnOrderThatOwesMoneyIsOfferedPayment holds the two funded cases apart:
// a captured card leaves the order committed and still owing, and a wholly
// store-credited one owes nothing and is not committed until it leaves pending.
func TestOnlyAnOrderThatOwesMoneyIsOfferedPayment(t *testing.T) {
	tests := []struct {
		name      string
		status    order.FulfillmentStatus
		committed bool
		owed      int64
		want      bool
	}{
		{name: "placed and unpaid", status: "pending", committed: false, owed: 106000, want: true},
		{name: "card captured, not yet picked", status: "pending", committed: true, owed: 106000, want: false},
		{name: "wholly paid from store credit", status: "pending", committed: false, owed: 0, want: false},
		{name: "cancelled", status: "cancelled", committed: false, owed: 106000, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &OrderView{
				Number: "GO-260101-000009", Status: tt.status,
				SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
				Committed: tt.committed, OwedCents: tt.owed, PaymentsEnabled: true,
			}
			html := renderToString(t, Order(layouts.Page{Title: "訂單"}, v))

			gotNotice := strings.Contains(html, "尚未付款")
			gotLink := strings.Contains(html, "/orders/GO-260101-000009/pay")
			if gotNotice != tt.want {
				t.Errorf("the 尚未付款 notice renders = %v, want %v", gotNotice, tt.want)
			}
			if gotLink != tt.want {
				t.Errorf("the payment link renders = %v, want %v", gotLink, tt.want)
			}
		})
	}
}

// TestADeliveredOrderOffersReturnAndShowsCredit holds that the canonical order
// page carries return and store-credit summary for a delivered order.
func TestADeliveredOrderOffersReturnAndShowsCredit(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	v := &OrderView{
		Number: "GO-260101-000012", Status: order.FulfillmentDelivered,
		SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
		CreditCents: 50000,
	}
	html := renderToString(t, Order(layouts.Page{Title: "訂單"}, v))
	if !strings.Contains(html, "/orders/GO-260101-000012/return") {
		t.Error("a delivered order does not link to its return form")
	}
	if !strings.Contains(html, i18n.T(ctx, i18n.KeyOrderCreditApplied)) {
		t.Error("a store-credited order does not show the credit row in its summary")
	}
	if !strings.Contains(html, "-NT$500") {
		t.Error("the credit row does not show how much store credit was applied")
	}
}

// TestADeliveredOrderLinksToItsWarrantyForm holds that a line links to the registration form from the day its
// parcel arrives, because cover starts when the goods reach somebody, and not before.
func TestADeliveredOrderLinksToItsWarrantyForm(t *testing.T) {
	line := pages.OrderLine{SKU: "S1", Name: "耳機", UnitCents: 100000, Quantity: 1, WarrantyMonths: 12}
	tests := []struct {
		name      string
		status    order.FulfillmentStatus
		delivered bool
		want      bool
	}{
		{name: "pending", status: "pending", want: false},
		{name: "picking", status: "picking", want: false},
		{name: "shipped", status: "shipped", want: false},
		{name: "delivered", status: "delivered", delivered: true, want: true},
		// Convenience-store pickup moves shipped to completed with nobody at the
		// counter to witness a handover, so completed also ends a delivery.
		{name: "completed", status: "completed", delivered: true, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &OrderView{
				Number: "GO-260101-000012", Status: tt.status,
				SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
				Committed: true, ShowWarrantyLink: true, Lines: []pages.OrderLine{line},
			}
			if tt.status != "pending" && tt.status != "picking" {
				parcel := OrderShipment{Carrier: shipping.CarrierBlackCat, Tracking: "T1", ShippedAt: orderNow.AddDate(0, 0, -4), Lines: []pages.OrderLine{line}}
				if tt.delivered {
					parcel.DeliveredAt, parcel.RescissionEnds, parcel.GoodwillEnds = orderNow.AddDate(0, 0, -3), orderDay(13), orderDay(20)
				}
				v.Shipments = []OrderShipment{parcel}
			}
			v.Now = orderNow
			html := renderToString(t, Order(layouts.Page{Title: "訂單"}, v))

			got := strings.Contains(html, "/account/warranty/GO-260101-000012")
			if got != tt.want {
				t.Errorf("the order page links its warranty form = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGuestTokenViewerDoesNotSeeWarrantyLink holds that a delivered order read
// through a guest access token must not expose the account-only registration door.
func TestGuestTokenViewerDoesNotSeeWarrantyLink(t *testing.T) {
	line := pages.OrderLine{SKU: "S1", Name: "耳機", UnitCents: 100000, Quantity: 1, WarrantyMonths: 12}
	v := &OrderView{
		Number: "GO-260101-000012", Status: order.FulfillmentDelivered,
		SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
		ShowWarrantyLink: false, Now: orderNow, Lines: []pages.OrderLine{line},
		Shipments: []OrderShipment{{
			Carrier: shipping.CarrierBlackCat, Tracking: "T1", ShippedAt: orderNow.AddDate(0, 0, -4), DeliveredAt: orderNow.AddDate(0, 0, -3),
			RescissionEnds: orderDay(13), GoodwillEnds: orderDay(20), Lines: []pages.OrderLine{line},
		}},
	}
	html := renderToString(t, Order(layouts.Page{Title: "訂單"}, v))
	if strings.Contains(html, "/account/warranty/GO-260101-000012") {
		t.Error("a guest-token viewer sees the account warranty registration link")
	}
}

// TestAFullyFundedOrderIsNotAskedToPay covers the funding term of
// AwaitingPayment. An order paid entirely from store credit, or zeroed by a
// 100% coupon, is legitimately 'pending' with NO payment row, so status and
// Committed together still read "unpaid" and only OwedCents tells them apart.
func TestAFullyFundedOrderIsNotAskedToPay(t *testing.T) {
	t.Parallel()

	funded := &OrderView{
		Number: "GO-260101-000012", Status: order.FulfillmentPending,
		SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
		// Not committed and nothing owed: paid in full from store credit.
		Committed: false, OwedCents: 0,
	}
	if funded.AwaitingPayment() {
		t.Error("an order that owes nothing is asked to pay; the pay link it " +
			"renders goes to a Stripe session that cannot be created")
	}

	html := renderToString(t, Order(layouts.Page{Title: "訂單"}, funded))
	if strings.Contains(html, "/orders/"+funded.Number+"/pay") {
		t.Error("the order page offers a payment link for an order that owes nothing")
	}

	// The control: same order, same status, same Committed, money still owed.
	owing := &OrderView{
		Number: "GO-260101-000013", Status: order.FulfillmentPending,
		SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
		Committed: false, OwedCents: 106000,
	}
	if !owing.AwaitingPayment() {
		t.Error("an order that still owes money is not offered a way to pay it")
	}
}

// TestWithoutPaymentsTheOrderPageLeadsNowhereNearThePayPage holds the other half
// of a loop: the pay page of a shop that takes no payment links to the order, so
// the order must not link back, and the pay page must still offer a way out.
func TestWithoutPaymentsTheOrderPageLeadsNowhereNearThePayPage(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	unpaid := &OrderView{
		Number: "GO-260101-000009", Status: order.FulfillmentPending,
		SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配", OwedCents: 106000,
	}
	html := renderToString(t, Order(layouts.Page{Title: "訂單"}, unpaid))
	if strings.Contains(html, "/orders/GO-260101-000009/pay") {
		t.Error("the order page links to a payment page the shop cannot serve")
	}
	if !strings.Contains(html, "尚未付款") {
		t.Error("the order page no longer says the order is unpaid")
	}

	pay := renderToString(t, payment.Pay(payment.PayMeta(ctx, "GO-260101-000009"), payment.PayView{Number: "GO-260101-000009"}))
	if !strings.Contains(pay, `href="/orders/GO-260101-000009"`) || !strings.Contains(pay, `href="/contact"`) {
		t.Errorf("the pay page of a shop with no payments offers no way out:\n%s", pay)
	}
}

// TestTheOrderPageStatesItsOwnState holds the eyebrow, the payment row and the
// amount due, each from the order's own facts.
func TestTheOrderPageStatesItsOwnState(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	render := func(v *OrderView) string {
		v.Number = "GO-260101-000020"
		return renderToString(t, Order(layouts.Page{Title: "訂單"}, v))
	}

	delivered := render(&OrderView{Status: order.FulfillmentDelivered, Committed: true, SubtotalCents: 100000, OwedCents: 100000})
	if !strings.Contains(delivered, `goen-pagehead__eyebrow">已送達<`) {
		t.Error("a delivered order's eyebrow does not say it was delivered")
	}
	if !strings.Contains(delivered, "付款狀態") || !strings.Contains(delivered, "付款完成") {
		t.Error("a delivered order does not say it is paid")
	}

	for _, tt := range []struct {
		name string
		view OrderView
		want PaymentState
	}{
		{"unpaid", OrderView{Status: order.FulfillmentPending, OwedCents: 100}, PaymentAwaiting},
		{"captured", OrderView{Status: order.FulfillmentPending, Committed: true, OwedCents: 100}, PaymentPaid},
		{"funded by credit", OrderView{Status: order.FulfillmentPending}, PaymentPaid},
		{"cancelled before paying", OrderView{Status: order.FulfillmentCancelled, OwedCents: 100}, PaymentNone},
		{"cancelled and refunded", OrderView{
			Status: order.FulfillmentCancelled, Timeline: []pages.OrderEvent{{Kind: "cancelled"}, {Kind: "refunded"}},
		}, PaymentRefunded},
		{"delivered and returned in full", OrderView{
			Status: order.FulfillmentDelivered, Committed: true, Returned: &OrderReturned{RefundCents: 100},
		}, PaymentRefunded},
		{"delivered with a partial refund", OrderView{
			Status: order.FulfillmentDelivered, Committed: true, Timeline: []pages.OrderEvent{{Kind: "refunded"}},
		}, PaymentPaid},
	} {
		if got := tt.view.PaymentState(); got != tt.want {
			t.Errorf("%s: payment state = %q, want %q", tt.name, got, tt.want)
		}
	}

	credited := render(&OrderView{
		Status: order.FulfillmentPending, SubtotalCents: 100000, ShippingCents: 6000, CreditCents: 106000, OwedCents: 0,
	})
	credit := strings.Index(credited, i18n.T(ctx, i18n.KeyOrderCreditApplied))
	due := strings.Index(credited, i18n.T(ctx, i18n.KeyOrderAmountDue))
	if credit < 0 || due < 0 || credit > due {
		t.Error("the store credit does not sit above the amount due")
	}
	if !strings.Contains(credited[due:], "NT$0") {
		t.Error("a fully credited order does not say NT$0 is due")
	}
}

func TestShowCancelKeepsTheOrderFundingAndHoldFacts(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 7, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name string
		view OrderView
		can  bool
		show bool
	}{
		{name: "unresolved return", view: OrderView{Status: order.FulfillmentPending, OwedCents: 100, PaymentReturnHint: true, HoldUntil: now.Add(time.Minute)}, can: true},
		{name: "plain", view: OrderView{Status: order.FulfillmentPending, OwedCents: 100, HoldUntil: now.Add(time.Minute)}, can: true, show: true},
		{name: "expired", view: OrderView{Status: order.FulfillmentPending, OwedCents: 100, PaymentReturnHint: true, HoldUntil: now.Add(-time.Nanosecond)}, can: true, show: true},
		{name: "equal deadline", view: OrderView{Status: order.FulfillmentPending, OwedCents: 100, PaymentReturnHint: true, HoldUntil: now}, can: true, show: true},
		{name: "unknown deadline", view: OrderView{Status: order.FulfillmentPending, OwedCents: 100, PaymentReturnHint: true}, can: true, show: true},
		{name: "expired checking", view: OrderView{Status: order.FulfillmentPending, OwedCents: 100, PaymentReturnHint: true, HoldUntil: now.Add(-time.Nanosecond), PaymentRefreshURL: "/orders/ORD-1?paid=1&confirmation=1"}, can: true},
		{name: "no hold checking", view: OrderView{Status: order.FulfillmentPending, OwedCents: 100, PaymentReturnHint: true, PaymentRefreshURL: "/orders/ORD-1?paid=1&confirmation=1"}, can: true},
		{name: "equal deadline checking", view: OrderView{Status: order.FulfillmentPending, OwedCents: 100, PaymentReturnHint: true, HoldUntil: now, PaymentRefreshURL: "/orders/ORD-1?paid=1&confirmation=1"}, can: true},
		{name: "checking without return marker", view: OrderView{Status: order.FulfillmentPending, OwedCents: 100, PaymentRefreshURL: "/orders/ORD-1?paid=1&confirmation=1"}, can: true},
		{name: "captured", view: OrderView{Status: order.FulfillmentPending, Committed: true, OwedCents: 100, PaymentReturnHint: true, HoldUntil: now.Add(time.Minute)}},
		{name: "store credit funded", view: OrderView{Status: order.FulfillmentPending, OwedCents: 0, PaymentReturnHint: true, HoldUntil: now.Add(time.Minute)}, can: true, show: true},
		{name: "cancelled", view: OrderView{Status: order.FulfillmentCancelled, OwedCents: 100, PaymentReturnHint: true, HoldUntil: now.Add(time.Minute)}},
		{name: "shipped", view: OrderView{Status: order.FulfillmentShipped, OwedCents: 100, PaymentReturnHint: true, HoldUntil: now.Add(time.Minute)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.view.Now = now
			if got := tt.view.CanCancel(); got != tt.can {
				t.Errorf("CanCancel() = %v, want %v", got, tt.can)
			}
			if got := tt.view.ShowCancel(); got != tt.show {
				t.Errorf("ShowCancel() = %v, want %v", got, tt.show)
			}
		})
	}
}

func TestTheOrderHistoryHidesAShippedEventsNote(t *testing.T) {
	t.Parallel()
	page := renderToString(t, Order(layouts.Page{Title: "o"}, &OrderView{
		Status: order.FulfillmentCancelled,
		Timeline: []pages.OrderEvent{
			{Kind: order.EventShipped, Note: "shipped-note-text"},
			{Kind: order.EventCancelled, Note: "cancelled-note-text"},
		},
	}))
	if strings.Contains(page, "shipped-note-text") {
		t.Error("the order history prints the shipped event's note")
	}
	if !strings.Contains(page, "cancelled-note-text") {
		t.Error("the order history dropped another event's note")
	}
}

// TestAPaidOrderIsNotBadgedAwaitingPaymentInTheAccount holds the same fact on
// both signed-in surfaces: the history badge and the detail page's notice. The
// history badge's half is internal/account's TestAccountOrderPaidIsNotAwaitingPayment.
func TestAPaidOrderIsNotBadgedAwaitingPaymentInTheAccount(t *testing.T) {
	view := &OrderView{
		Number: "GO-260101-000010", Status: order.FulfillmentPending,
		SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
		Committed: true, OwedCents: 106000,
	}
	html := renderToString(t, Order(layouts.Page{Title: "訂單"}, view))
	if strings.Contains(html, "尚未付款") {
		t.Error("the canonical order page tells a paid customer their order is unpaid")
	}
}
