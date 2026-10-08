package pages

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
)

type PayLine struct {
	Name      string
	Label     string
	UnitCents int64
	Quantity  int32
}

func (l PayLine) UnitPrice() string { return twd(l.UnitCents) }

func (l PayLine) LineTotal() string { return twd(l.UnitCents * int64(l.Quantity)) }

func (l PayLine) QuantityText() string { return strconv.FormatInt(int64(l.Quantity), 10) }

type PayClosure int

const (
	PayOpen PayClosure = iota
	// PayWindowClosed is pending, but no live hold is long enough for a Checkout
	// Session; the hold sweeper cancels it once the hold lapses.
	PayWindowClosed
	PayOrderCancelled
)

// PayHold is the stored span of the order's stock hold, which the page reads and
// never computes. StartBy is zero where the page names no payment deadline,
// because a session is already open or the window has closed. A zero PlacedAt
// means no hold is stated.
type PayHold struct {
	PlacedAt, StartBy, Until time.Time
	// CancelledAt is when the hold sweeper cancelled the order unpaid; it is
	// zero otherwise.
	CancelledAt time.Time
}

// Lapsed means the hold ended with the order unpaid and the shop cancelled it.
func (h PayHold) Lapsed() bool { return !h.CancelledAt.IsZero() }

type PayView struct {
	Number         string
	TotalCents     int64
	Email          string
	Lines          []PayLine
	Enabled        bool
	Sandbox        bool
	Cancelled      bool
	Closure        PayClosure
	Hold           PayHold
	ShippingName   string
	ShippingCents  int64
	DiscountCents  int64
	DiscountReason string
	CreditCents    int64
}

// Facts is when the order was placed and the amount due, or for a lapsed hold what became of the order.
func (h PayHold) Facts(ctx context.Context, totalCents int64) []components.Stat {
	if h.PlacedAt.IsZero() {
		return nil
	}
	placed := components.Stat{Label: i18n.T(ctx, i18n.KeyPayFactPlaced), Value: payClock(h.PlacedAt)}
	if h.Lapsed() {
		return []components.Stat{
			placed,
			{Label: i18n.T(ctx, i18n.KeyPayFactCancelled), Value: payClock(h.CancelledAt), Note: i18n.T(ctx, i18n.KeyPayFactLapsed)},
			{Label: i18n.T(ctx, i18n.KeyPayFactCharged), Value: components.StatMoney(0)},
		}
	}
	return []components.Stat{placed, {Label: i18n.T(ctx, i18n.KeyPayFactAmountDue), Value: components.StatMoney(totalCents)}}
}

// Window is the hold in words while it runs: the deadline to start paying and then when the hold ends, or the
// hold's end first where no deadline is named. ok is false with no stored hold, or once it has lapsed.
func (h PayHold) Window(ctx context.Context) (lead, note string, ok bool) {
	if h.PlacedAt.IsZero() || h.Lapsed() {
		return "", "", false
	}
	until := shoptime.ClockText(h.Until)
	if h.StartBy.IsZero() {
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyPayReserved), until), i18n.T(ctx, i18n.KeyPayReservedTail), true
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyPayDeadline), shoptime.ClockText(h.StartBy)), fmt.Sprintf(i18n.T(ctx, i18n.KeyPayReservedNote), until), true
}

// EyebrowKey names the order's status; an unpaid order is 待付款 whether or not a payment can start, because
// 完成付款 above an order that is not paid reads as already paid.
func (v *PayView) EyebrowKey() i18n.Key {
	if v.Closure == PayOrderCancelled {
		return i18n.KeyStatusCancelled
	}
	return i18n.KeyStatusAwaitingPayment
}

// HoldWindow is the hold in words as this page can keep it: a deadline to start paying is named only while a
// payment can start, so with payments off the page leads with when the items are released.
func (v *PayView) HoldWindow(ctx context.Context) (lead, note string, ok bool) {
	h := v.Hold
	if !v.Payable() {
		h.StartBy = time.Time{}
	}
	return h.Window(ctx)
}

// Payable is true while a payment can start or resume here.
func (v *PayView) Payable() bool { return !v.Closed() && v.Enabled }

func (v *PayView) Closed() bool { return v.Closure != PayOpen }

func (v *PayView) ClosedTitle() i18n.Key {
	if v.Closure == PayOrderCancelled {
		return i18n.KeyPayRefusedTitle
	}
	return i18n.KeyPayWindowClosedTitle
}

func (v *PayView) ClosedBody() i18n.Key {
	if v.Closure == PayOrderCancelled {
		return i18n.KeyOrderCancelled
	}
	return i18n.KeyPayWindowClosedBody
}

func (v *PayView) Subtotal() string {
	var n int64
	for _, l := range v.Lines {
		n += l.UnitCents * int64(l.Quantity)
	}
	return twd(n)
}

func (v *PayView) Shipping(ctx context.Context) string {
	if v.ShippingCents == 0 {
		return i18n.T(ctx, i18n.KeyFreeShipping)
	}
	return twd(v.ShippingCents)
}

func (v *PayView) Discounted() bool { return v.DiscountCents > 0 }

func (v *PayView) Discount() string { return "-" + twd(v.DiscountCents) }

func (v *PayView) UsedCredit() bool { return v.CreditCents > 0 }

func (v *PayView) Credit() string { return "-" + twd(v.CreditCents) }

func (v *PayView) Total() string { return twd(v.TotalCents) }

// Action posts to the order's pay route; the amount is recomputed server-side.
func (v *PayView) Action() string { return "/orders/" + v.Number + "/pay" }

func PayMeta(ctx context.Context, number string) layouts.Page {
	return layouts.Page{Title: fmt.Sprintf(i18n.T(ctx, i18n.KeyPayMeta), number)}
}

func payClock(t time.Time) components.StatValue {
	return components.StatClock(shoptime.ClockText(t)).WithDatetime(shoptime.InputMinute(t))
}
