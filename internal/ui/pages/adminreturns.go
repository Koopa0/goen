package pages

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
)

// AdminReturn is one row in the back-office return queue.
type AdminReturn struct {
	Lines       []AdminReturnLine
	ID          string
	OrderNumber string
	Status      string
	StatusText  string
	Reason      string
	Units       int32
	AmountCents int64
	CreatedAt   string
	// Window is "within", "after" or "undelivered" against Consumer Protection
	// Act §19 I's seven days from receipt.
	Window  string
	Decided bool
	// Decided and settled are separate facts: approval is committed before the
	// provider or credit ledger completes what the shop owes.
	PayoutOutstanding bool
	// PayoutBlocked says the outstanding card refund is terminal at the provider
	// and the retry door cannot move it.
	PayoutBlocked bool
}

// CanRetryPayout reports whether the approved decision has money left behind a
// resume-safe door.
func (r AdminReturn) CanRetryPayout() bool {
	return r.Decided && r.PayoutOutstanding && !r.PayoutBlocked
}

// PayoutStranded reports whether a person must settle the refund outside goen.
func (r AdminReturn) PayoutStranded() bool {
	return r.Decided && r.PayoutOutstanding && r.PayoutBlocked
}

// AwaitingGoods reports whether an approved parcel is still unaccounted for.
func (r AdminReturn) AwaitingGoods() bool {
	if r.Status != "approved" {
		return false
	}
	for _, l := range r.Lines {
		if !l.Inspected {
			return true
		}
	}
	return false
}

// CanComplete reports whether every line has been inspected.
func (r AdminReturn) CanComplete() bool {
	if r.Status != "approved" || len(r.Lines) == 0 {
		return false
	}
	for _, l := range r.Lines {
		if !l.Inspected {
			return false
		}
	}
	return true
}

// RestockedUnitsText is how many units this return put back on the shelf.
func (r AdminReturn) RestockedUnitsText() string {
	var n int32
	for _, l := range r.Lines {
		n += l.Restocked
	}
	return strconv.FormatInt(int64(n), 10)
}

// Rescission reports whether this request is inside the statutory seven days.
func (r AdminReturn) Rescission() bool { return r.Window == "within" }

// WindowText names the window in the reader's language.
func (r AdminReturn) WindowText(ctx context.Context) string {
	switch r.Window {
	case "within":
		return i18n.T(ctx, i18n.KeyAdminReturnWindowWithin)
	case "after":
		return i18n.T(ctx, i18n.KeyAdminReturnWindowAfter)
	case "undelivered":
		return i18n.T(ctx, i18n.KeyAdminReturnWindowUndelivered)
	default:
		panic("pages: unknown rescission window: " + r.Window)
	}
}

// Amount is what approving it would refund.
func (r AdminReturn) Amount() string { return twd(r.AmountCents) }

// UnitsText is how many items are being sent back.
func (r AdminReturn) UnitsText() string { return strconv.FormatInt(int64(r.Units), 10) }

// Action is where a decision on this return posts.
func (r AdminReturn) Action() string { return "/admin/returns/" + r.ID + "/decide" }

// InspectAction and CompleteAction are the tail's two forms.
func (r AdminReturn) InspectAction() string { return "/admin/returns/" + r.ID + "/inspect" }

// CompleteAction closes an inspected return.
func (r AdminReturn) CompleteAction() string { return "/admin/returns/" + r.ID + "/complete" }

// AdminReturnsView is the return queue.
type AdminReturnsView struct {
	Rows   []AdminReturn
	Notice string
}

// Empty reports whether there is nothing to show.
func (v AdminReturnsView) Empty() bool { return len(v.Rows) == 0 }

// AdminReturnLine is one item in a return request.
type AdminReturnLine struct {
	SKU         string
	Name        string
	Label       string
	UnitCents   int64
	Quantity    int32
	OrderLineID string
	Inspected   bool
	Received    int32
	Restocked   int32
	Note        string
	Restockable bool
}

// ReceivedText and RestockedText are the figures as the form's default values.
func (l AdminReturnLine) ReceivedText() string {
	return strconv.FormatInt(int64(l.Received), 10)
}

// RestockedText is how many went back on the shelf.
func (l AdminReturnLine) RestockedText() string {
	return strconv.FormatInt(int64(l.Restocked), 10)
}

// MaxQuantityText bounds the received input to what was claimed.
func (l AdminReturnLine) MaxQuantityText() string {
	return strconv.FormatInt(int64(l.Quantity), 10)
}

// Shortfall reports whether fewer units arrived than were claimed.
func (l AdminReturnLine) Shortfall() bool { return l.Inspected && l.Received < l.Quantity }

// Scrapped reports whether something came back that could not be resold.
func (l AdminReturnLine) Scrapped() bool { return l.Inspected && l.Restocked < l.Received }

// Line is the item as one row of text.
func (l AdminReturnLine) Line() string {
	name := l.Name
	if l.Label != "" {
		name += " · " + l.Label
	}
	return name + " × " + strconv.FormatInt(int64(l.Quantity), 10)
}

// UnitPrice is what one of them cost.
func (l AdminReturnLine) UnitPrice() string { return twd(l.UnitCents) }
