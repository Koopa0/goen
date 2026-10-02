package pages

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// PayLine is one item on the payment page, as the order recorded it.
type PayLine struct {
	Name      string
	Label     string
	UnitCents int64
	Quantity  int32
}

// UnitPrice is the agreed price per unit.
func (l PayLine) UnitPrice() string { return twd(l.UnitCents) }

// LineTotal is what the line comes to.
func (l PayLine) LineTotal() string { return twd(l.UnitCents * int64(l.Quantity)) }

// QuantityText is how many were ordered.
func (l PayLine) QuantityText() string { return strconv.FormatInt(int64(l.Quantity), 10) }

// PayClosure says why the payment page offers no way to pay.
type PayClosure int

const (
	// PayOpen can start or resume a payment.
	PayOpen PayClosure = iota
	// PayWindowClosed is still pending, but no live hold is long enough for a
	// Checkout Session; the hold sweeper cancels it once the hold lapses.
	PayWindowClosed
	// PayOrderCancelled has been called off.
	PayOrderCancelled
)

// PayView is the page that hands a customer over to the card form.
type PayView struct {
	Number     string
	TotalCents int64
	Email      string
	Lines      []PayLine
	Enabled    bool
	Sandbox    bool
	Cancelled  bool
	Closure    PayClosure
	// StartBy is the last minute a new Checkout Session can start, empty when
	// the page resumes one already open.
	StartBy string
	// The rest of the order's own breakdown, so the rows between the lines and
	// what is owed are the ones its order page lists.
	ShippingName   string
	ShippingCents  int64
	DiscountCents  int64
	DiscountReason string
	CreditCents    int64
}

// EyebrowKey names the page by what the shopper can do here: 完成付款 only
// while a payment can start or resume.
func (v PayView) EyebrowKey() i18n.Key {
	switch {
	case v.Closure == PayOrderCancelled:
		return i18n.KeyStatusCancelled
	case v.Closed() || !v.Enabled:
		return i18n.KeyStatusAwaitingPayment
	default:
		return i18n.KeyPayEyebrow
	}
}

// Closed reports whether no payment can start or resume here.
func (v PayView) Closed() bool { return v.Closure != PayOpen }

// ClosedTitle says that the order cannot be paid.
func (v PayView) ClosedTitle() i18n.Key {
	if v.Closure == PayOrderCancelled {
		return i18n.KeyPayRefusedTitle
	}
	return i18n.KeyPayWindowClosedTitle
}

// ClosedBody says what became, or will become, of the order.
func (v PayView) ClosedBody() i18n.Key {
	if v.Closure == PayOrderCancelled {
		return i18n.KeyOrderCancelled
	}
	return i18n.KeyPayWindowClosedBody
}

// Subtotal is what the lines came to.
func (v PayView) Subtotal() string {
	var n int64
	for _, l := range v.Lines {
		n += l.UnitCents * int64(l.Quantity)
	}
	return twd(n)
}

// Shipping is what delivery cost.
func (v PayView) Shipping(ctx context.Context) string {
	if v.ShippingCents == 0 {
		return i18n.T(ctx, i18n.KeyFreeShipping)
	}
	return twd(v.ShippingCents)
}

// Discounted reports whether a discount came off.
func (v PayView) Discounted() bool { return v.DiscountCents > 0 }

// Discount is what came off, as a negative figure.
func (v PayView) Discount() string { return "-" + twd(v.DiscountCents) }

// UsedCredit reports whether store credit paid part of the order.
func (v PayView) UsedCredit() bool { return v.CreditCents > 0 }

// Credit is what the store credit took off, as a negative figure.
func (v PayView) Credit() string { return "-" + twd(v.CreditCents) }

// Total is what is owed.
func (v PayView) Total() string { return twd(v.TotalCents) }

// Action is where the form posts; the amount is recomputed server-side.
func (v PayView) Action() string { return "/orders/" + v.Number + "/pay" }

// PayMeta is the chrome view model for the payment page.
func PayMeta(ctx context.Context, number string) layouts.Page {
	return layouts.Page{Title: fmt.Sprintf(i18n.T(ctx, i18n.KeyPayMeta), number)}
}
