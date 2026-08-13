package pages

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
)

// AdminReturn is one row in the back-office return queue.
type AdminReturn struct {
	// Lines is WHAT is being sent back. The page showed a count and an amount
	// and nothing else, so a staff member decided a return without being able
	// to see what was in it — and the query for this existed, unused.
	Lines       []AdminReturnLine
	ID          string
	OrderNumber string
	Status      string
	StatusText  string
	Reason      string
	Units       int32
	AmountCents int64
	CreatedAt   string
	// Window says whether this is a STATUTORY rescission or a goodwill return:
	// "within", "after" or "undelivered". The page showed neither, so a staff
	// member deciding a return could not tell a request the shop may not refuse
	// from one that is entirely theirs to decline — 消保法 §19 I gives seven days
	// from receipt and §19 V voids any agreement otherwise.
	//
	// It INFORMS and does not restrict. Decide still accepts "rejected" for any
	// open request, because an in-window rescission is not auto-approved either:
	// §19-2 gives the trader fifteen days to refund after the goods come BACK, so
	// "the parcel never arrived" is a legitimate refusal and only a person can
	// know it. What was missing was the fact on screen, not a new rule.
	Window  string
	Decided bool
}

// AwaitingGoods reports whether this return is approved and the parcel has not
// been accounted for — the state the queue existed to leave out.
//
// A return used to stop at 同意: the money went back, and the goods were in a
// state nothing recorded and nobody could act on. This is the work that follows.
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

// CanComplete reports whether every line has been inspected, which is what
// return_requests_completed_is_inspected demands before the status may move.
// The database is the authority; this only decides whether to offer the button.
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
//
// Shown beside the close button, because that is the moment a staff member is
// asked to agree the return is finished — and "three came back, two went on the
// shelf" is the fact they are agreeing to.
func (r AdminReturn) RestockedUnitsText() string {
	var n int32
	for _, l := range r.Lines {
		n += l.Restocked
	}
	return strconv.FormatInt(int64(n), 10)
}

// Rescission reports whether this request is inside the statutory seven days,
// so the template can mark it.
func (r AdminReturn) Rescission() bool { return r.Window == "within" }

// WindowText names the window in the reader's language.
//
// It takes a ctx now. The comment here used to say "no i18n: /admin is the staff
// of one Taiwanese shop, which is the documented category exclusion" — and the
// exclusion it cited has been deleted, which is the hazard of a comment that
// cites a decision made somewhere else.
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
	SKU       string
	Name      string
	Label     string
	UnitCents int64
	Quantity  int32
	// OrderLineID names this line to the inspection form. The form posts per
	// line, because a parcel of three can come back as two sellable and one
	// broken and a single figure for the request cannot say that.
	OrderLineID string
	// Inspected, and what was found. Absent is "nobody has opened the parcel",
	// which is different from "opened it, nothing was in it" — one is work
	// outstanding and the other is a conversation with the customer.
	Inspected bool
	Received  int32
	Restocked int32
	Note      string
	// Restockable is whether the unit has a variant to go back into at all.
	// order_lines.variant_id is nullable so a line survives its variant being
	// deleted, and offering a restock control the write would refuse is worse
	// than not offering one.
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

// Shortfall reports whether fewer units arrived than the customer said they were
// sending. It is the one difference a staff member has to act on rather than
// merely record, so the page says it rather than leaving two numbers to compare.
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
