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

// TestTheOrderPageShowsTheDiscountAndWhy renders the page and looks for the row.
//
// The failure this locks is silent: the discount used to be absent from the order
// summary entirely, so subtotal plus shipping did not equal the total and nothing
// accounted for the difference. Nothing about the view model would have told
// anybody — the numbers were all there, and one of them was simply not rendered.
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

// renderToString runs a component and returns its HTML.
func renderToString(t *testing.T, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(i18n.WithLocale(t.Context(), i18n.ZhHant), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}
