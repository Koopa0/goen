package pages

import (
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// TestCheckoutLinkKeepsTheOtherChoice holds that the two choosers compose.
//
// Both choosers are links, and each has to carry what the other chose: a
// customer who picked 超商取貨 and then a saved address must not be sent back
// to 宅配 by the second link.
func TestCheckoutLinkKeepsTheOtherChoice(t *testing.T) {
	t.Parallel()

	v := &CheckoutView{Chosen: "ship-1", ChosenAddress: "addr-1"}

	if got, want := v.CheckoutLink("address", "addr-2"),
		"/checkout?address=addr-2&ship=ship-1"; got != want {
		t.Errorf("the address link dropped the method: %q, want %q", got, want)
	}
	if got, want := v.CheckoutLink("ship", "ship-2"),
		"/checkout?address=addr-1&ship=ship-2"; got != want {
		t.Errorf("the method link dropped the address: %q, want %q", got, want)
	}

	// Nothing chosen yet: the link carries only what it sets, rather than empty
	// parameters that would read as "the customer picked nothing".
	empty := &CheckoutView{}
	if got, want := empty.CheckoutLink("ship", "ship-2"), "/checkout?ship=ship-2"; got != want {
		t.Errorf("a first choice carried baggage: %q, want %q", got, want)
	}
}

// TestTheAddressBookIsOnlyOfferedForAnAddress. A saved street address means
// nothing to a parcel going to a convenience store, and offering one there
// would fill fields the form is not showing.
func TestTheAddressBookIsOnlyOfferedForAnAddress(t *testing.T) {
	t.Parallel()

	book := []SavedAddress{{ID: "a"}}
	for _, tt := range []struct {
		name        string
		destination string
		book        []SavedAddress
		want        bool
	}{
		{"an address order with a book", "address", book, true},
		{"a pickup order with a book", "pickup_point", book, false},
		{"an address order with no book", "address", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := &CheckoutView{Destination: tt.destination, SavedAddresses: tt.book}
			if got := v.OffersTheAddressBook(); got != tt.want {
				t.Errorf("OffersTheAddressBook() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCanCancelIsAboutFundingNotJustStatus holds when the button is offered.
//
// 'pending' does not mean unpaid. A webhook captures the money minutes before
// the shop moves the order to picking, and in that window the status is still
// pending — offering a cancel button there is a control that can only say no,
// because CancelOrderByCustomer refuses a committed order.
func TestCanCancelIsAboutFundingNotJustStatus(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name      string
		status    string
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

// TestTheCheckoutShowsTheRequotedFigure holds which of the two figures the
// summary renders.
//
// The method chooser is a link and the address is a form below it, so the fee
// it shows is a MAINLAND estimate — the postal code does not exist yet. Once
// the server has priced the real address, that figure has to win, or the
// summary and the order disagree.
func TestTheCheckoutShowsTheRequotedFigure(t *testing.T) {
	t.Parallel()

	choices := []ShippingChoice{{VersionID: "v1", FeeCents: 8000}}

	before := &CheckoutView{Shipping: choices, Chosen: "v1"}
	if got := before.ShippingFeeCents(); got != 8000 {
		t.Errorf("before an address is typed the estimate is %d, want 8000", got)
	}

	after := &CheckoutView{Shipping: choices, Chosen: "v1", QuotedShippingCents: 28000}
	if got := after.ShippingFeeCents(); got != 28000 {
		t.Errorf("after re-quoting the summary shows %d, want 28000 — it is still "+
			"showing the mainland estimate", got)
	}
}

// TestTheCheckoutSummaryAddsUpTheWayPlaceOrderDoes holds the checkout total
// against the arithmetic that actually writes the order.
//
// TotalCents' own doc comment says "the same arithmetic PlaceOrder does. It has
// to be", and the 離島 path is where the two most easily part company. The two
// ways they can are both here:
//
//   - A 免運 coupon pays the base rate and never the crossing, because
//     cart.priceOrder keeps the surcharge deliberately (「免運 covers the base
//     rate the shop advertises, never the 離島 surcharge a carrier charges on
//     top of it」, from shipping_version_zones' own column comment). Zeroing the
//     WHOLE shipping figure here promises subtotal-discount on a page whose
//     order is written at subtotal+surcharge-discount, and the customer meets
//     the difference on the payment page.
//   - The fee row states the BASE fee, because the surcharge already has a
//     second row of its own. Printing the quote's TOTAL there — which contains
//     the surcharge — shows a reader 280 and 200 for a delivery charge of 280.
//
// The expected values are hand-computed literals, not expressions over the
// fixture, so the test states the arithmetic rather than restating the code.
func TestTheCheckoutSummaryAddsUpTheWayPlaceOrderDoes(t *testing.T) {
	t.Parallel()

	// 宅配 at NT$80 to 金門, which the carrier charges NT$200 more to reach.
	// Two items at NT$500.
	base := func() *CheckoutView {
		return &CheckoutView{
			Cart:                CartView{SubtotalCents: 100000},
			Shipping:            []ShippingChoice{{VersionID: "v1", FeeCents: 8000}},
			Chosen:              "v1",
			QuotedShippingCents: 28000, // 8000 base + 20000 surcharge
			SurchargeCents:      20000,
		}
	}

	t.Run("the two summary rows do not double-count the surcharge", func(t *testing.T) {
		t.Parallel()
		v := base()
		if got := v.BaseShippingCents(); got != 8000 {
			t.Errorf("the 運費 row shows %d, want 8000 — the 離島加價 row states the "+
				"other 20000 on its own line, so printing the combined figure here "+
				"reads as 28000 + 20000", got)
		}
		if got := v.TotalCents(); got != 128000 {
			t.Errorf("total = %d, want 128000 (100000 + 8000 + 20000)", got)
		}
	})

	t.Run("a 免運 coupon pays the base rate and not the crossing", func(t *testing.T) {
		t.Parallel()
		v := base()
		v.CouponFreeShipping = true
		if got := v.TotalCents(); got != 120000 {
			t.Errorf("total with a free-shipping coupon = %d, want 120000 "+
				"(100000 + 0 base + 20000 surcharge) — zeroing the surcharge too "+
				"makes the page promise a figure PlaceOrder will not write, and the "+
				"customer meets the difference on the payment page", got)
		}
		if !v.ShipsFree() {
			t.Error("the fee row does not read 免運 with a free-shipping coupon")
		}
	})

	t.Run("no surcharge, no coupon", func(t *testing.T) {
		t.Parallel()
		v := &CheckoutView{
			Cart:     CartView{SubtotalCents: 100000},
			Shipping: []ShippingChoice{{VersionID: "v1", FeeCents: 8000}},
			Chosen:   "v1",
		}
		if got := v.TotalCents(); got != 108000 {
			t.Errorf("total = %d, want 108000", got)
		}
	})
}

// TestTheOrderPageShowsTheDiscountAndWhy renders the page and looks for the row.
//
// The failure this locks is silent: a discount absent from the order summary
// leaves subtotal plus shipping not equal to the total with nothing accounting
// for the difference, so somebody reading their own receipt cannot tell whether
// they were overcharged. Nothing about the view model says so — every number is
// there, and one of them is simply not rendered.
//
// Asserted against the HTML rather than against Discounted(), which is
// `DiscountCents > 0` and would be a tautology to test.
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

	// And an order with no discount renders no row at all, rather than a stray
	// "折扣 -NT$0".
	plain := &OrderView{
		Number: "GO-260101-000002", Status: "pending",
		SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
	}
	if got := renderToString(t, Order(layouts.Page{Title: "訂單"}, plain)); strings.Contains(got, "-NT$0") {
		t.Error("an order with no discount renders a zero discount row")
	}
}

// TestOnlyAnOrderThatOwesMoneyIsOfferedPayment holds the sentence a paid
// customer must not read on the first page they see after paying.
//
// Deciding it from `Status == "pending"` alone is what puts it there: an order
// stays pending from the capture until a human at the shop picks it, so 尚未付款
// and a 前往付款 link would sit on an order that is paid for — and Stripe's
// success_url returns the customer to exactly this page, with the emailed
// receipt linking back to it.
//
// Asserted through the RENDER rather than the method, for the reason
// TestTheOrderPageShowsTheDiscountAndWhy is: the view model can hold Committed
// correctly — CanCancel reads it, three lines away — while the template says the
// wrong thing, and only a render can see that.
//
// The two funded cases are separate rows because neither signal covers the
// other: a captured card leaves the order committed and still owing, and a fully
// store-credited order owes nothing and is not committed until it leaves pending.
func TestOnlyAnOrderThatOwesMoneyIsOfferedPayment(t *testing.T) {
	tests := []struct {
		name      string
		status    string
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
				Committed: tt.committed, OwedCents: tt.owed,
			}
			html := renderToString(t, Order(layouts.Page{Title: "訂單"}, v))

			// The words and the link are asserted separately: removing one and
			// leaving the other is still a page that lies.
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

// TestAPaidOrderIsNotBadgedAwaitingPaymentInTheAccount is the same fact on the
// two surfaces a signed-in customer reaches it from.
//
// The history list badges a pending order from AccountOrder.StatusText and the
// detail page carries the notice, so the funding columns have to reach both. An
// account query that selects none leaves that surface with no signal to be right
// with: every pending order is badged 待付款, captured or not.
func TestAPaidOrderIsNotBadgedAwaitingPaymentInTheAccount(t *testing.T) {
	paid := AccountOrder{
		Number: "GO-260101-000010", Status: "pending", PlacedAt: "2026-01-01",
		TotalCents: 106000, LineCount: 1, Committed: true, OwedCents: 106000,
	}
	unpaid := AccountOrder{
		Number: "GO-260101-000011", Status: "pending", PlacedAt: "2026-01-01",
		TotalCents: 106000, LineCount: 1, Committed: false, OwedCents: 106000,
	}
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	if got := paid.StatusText(ctx); got != "付款完成" {
		t.Errorf("a captured order is badged %q in the history, want 付款完成", got)
	}
	// The control: without it, a StatusText that said 付款完成 for everything
	// would pass the line above.
	if got := unpaid.StatusText(ctx); got != "待付款" {
		t.Errorf("an unpaid order is badged %q, want 待付款", got)
	}

	// And the detail page, which carries the notice rather than the badge. The
	// funding fields have to travel with the status — StatusText builds an
	// AccountOrder literal, and a field left out of one takes the zero value,
	// which reads as unpaid.
	view := &AccountOrderView{
		Number: "GO-260101-000010", Status: "pending",
		SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
		Committed: true, OwedCents: 106000,
	}
	html := renderToString(t, AccountOrderPage(layouts.Page{Title: "訂單"}, view))
	if strings.Contains(html, "尚未付款") {
		t.Error("the account's own order page tells a paid customer their order is unpaid")
	}
	if got := view.StatusText(ctx); got != "付款完成" {
		t.Errorf("the detail page badges the order %q, want 付款完成 — the funding "+
			"fields did not travel into the AccountOrder literal", got)
	}
}

// TestTheOrderNotFoundPageOffersAWayThrough holds the one 404 that has one.
//
// An order page is shown to the browser that placed the order or to the account
// that owns it; anything else is this 404. The guest it is most often shown to —
// somebody who cleared their cookies, or opened the confirmation email on their
// phone — has no account to sign in to, so a page offering /signin alone is no
// answer for them, and /orders/find is built for exactly that reader. Both are
// offered, because the reader is one of two people and the page cannot tell
// which.
//
// Asserted through the render, because there is no view model to be wrong:
// /orders/find resolves whether or not anything points at it, and only a page
// can be missing a link.
func TestTheOrderNotFoundPageOffersAWayThrough(t *testing.T) {
	html := renderToString(t, OrderNotFound(layouts.Page{Title: "404"}))

	for _, want := range []string{"/orders/find", "/signin"} {
		if !strings.Contains(html, want) {
			t.Errorf("the order 404 does not link to %s; the reader is one of two "+
				"people and the page cannot tell which", want)
		}
	}
}

// TestADeliveredOrderLinksToItsWarrantyForm closes the dead end every signed-in
// customer otherwise meets.
//
// /account/warranty/{number} carries the ONLY form that registers a unit, and
// the account nav reaches the LIST rather than the form — the list's own copy
// says 「從訂單頁進去登錄」, so the order page is where the link has to be. Without
// it the only way in is typing a URL the site never displays.
//
// TestEveryHardCodedLinkResolvesToARoute cannot see this by construction. It
// asks link→route, the route IS linked one level up, and a templated href
// carrying an order number is skipped by its parser either way.
//
// The table asserts the CONTRACT and not the implementation. Cover starts when
// goods reach somebody and the term is computed from
// order_shipments.delivered_at, so a row saying shipped → true would offer the
// link at DISPATCH, which is a different moment: on a 'shipped' order the form
// exists and can register nothing. A table written from the implementation
// asserts what the code does; only one written from the contract can disagree
// with it.
func TestADeliveredOrderLinksToItsWarrantyForm(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   bool
	}{
		// Cover starts when goods reach somebody, which is what registration
		// itself requires — so before DELIVERY the link would lead to a page
		// whose every line says "not arrived yet".
		{name: "pending", status: "pending", want: false},
		{name: "picking", status: "picking", want: false},
		{name: "shipped", status: "shipped", want: false},
		{name: "delivered", status: "delivered", want: true},
		// 超商取貨 moves shipped → completed with nobody at the counter to
		// witness a handover, so 'completed' is the other end of a delivery and
		// not a state beyond it. Leaving it out would hide the form from a whole
		// channel — the mistake the delivered_at stamp itself made first.
		{name: "completed", status: "completed", want: true},
		{name: "cancelled", status: "cancelled", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &AccountOrderView{
				Number: "GO-260101-000012", Status: tt.status,
				SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
				Committed: true,
			}
			html := renderToString(t, AccountOrderPage(layouts.Page{Title: "訂單"}, v))

			got := strings.Contains(html, "/account/warranty/GO-260101-000012")
			if got != tt.want {
				t.Errorf("the order page links its warranty form = %v, want %v", got, tt.want)
			}
		})
	}
}

// renderToString runs a component and returns its HTML.
func renderToString(t *testing.T, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(i18n.WithLocale(t.Context(), i18n.ZhHant), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}
