package pages

import (
	"context"
	"fmt"
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
	// CardRefundCents and CreditRefundCents are the frozen source allocation.
	// Both stay zero until approval freezes them, so the queue does not invent
	// a channel for an open request.
	CardRefundCents   int64
	CreditRefundCents int64
	CreatedAt         string
	// Window is "within", "goodwill", "after", "undelivered" or "mixed":
	// the statutory seven days, the shop's advertised days 8–14, later
	// than that, a parcel whose window has not started, or a request whose
	// lines disagree. Counted from the request clock against each line.
	Window            string
	AssessmentVersion int32
	AssessmentBasis   string
	AssessedAt        string
	// Resolution is the staff note still in the decide form. It is empty on a
	// first paint and holds the submitted text after a 422 so the reason is
	// not lost.
	Resolution string
	Decided    bool
	// Decided and settled are separate facts: approval is committed before the
	// provider or credit ledger completes what the shop owes.
	PayoutOutstanding bool
	// PayoutBlocked says the durable payout facts do not fit their frozen source
	// allocation. Known terminal provider attempts are not blocked: retry appends
	// a new generation with a fresh idempotency key.
	PayoutBlocked bool
}

// CanRetryPayout reports whether the approved decision has money left behind a
// resume-safe door.
func (r *AdminReturn) CanRetryPayout() bool {
	return r.Decided && r.PayoutOutstanding && !r.PayoutBlocked
}

// PayoutStranded reports whether a person must repair inconsistent durable
// payout facts before goen can safely retry.
func (r *AdminReturn) PayoutStranded() bool {
	return r.Decided && r.PayoutOutstanding && r.PayoutBlocked
}

// AwaitingGoods reports whether an approved parcel is still unaccounted for.
func (r *AdminReturn) AwaitingGoods() bool {
	if r.Status != "approved" {
		return false
	}
	for i := range r.Lines {
		if !r.Lines[i].Inspected {
			return true
		}
	}
	return false
}

// CanComplete reports whether every line has been inspected.
func (r *AdminReturn) CanComplete() bool {
	if r.Status != "approved" || len(r.Lines) == 0 {
		return false
	}
	for i := range r.Lines {
		if !r.Lines[i].Inspected {
			return false
		}
	}
	return true
}

// RestockedUnitsText is how many units this return put back on the shelf.
func (r *AdminReturn) RestockedUnitsText() string {
	var n int32
	for i := range r.Lines {
		n += r.Lines[i].Restocked
	}
	return strconv.FormatInt(int64(n), 10)
}

// ReturnLineWindowText names one line's window without copying a queue row.
func ReturnLineWindowText(ctx context.Context, window string) string {
	return (&AdminReturn{Window: window}).WindowText(ctx)
}

// Rescission reports whether this request is inside the statutory seven days.
func (r *AdminReturn) Rescission() bool { return r.Window == "within" }

// Goodwill reports whether this request is inside the shop's advertised
// days 8–14. Entitlement still depends on unused-and-complete facts.
func (r *AdminReturn) Goodwill() bool { return r.Window == "goodwill" }

// Late reports whether this request was filed after the advertised 14 days.
func (r *AdminReturn) Late() bool { return r.Window == "after" }

// Mixed reports whether the returned lines fall in more than one window.
func (r *AdminReturn) Mixed() bool { return r.Window == "mixed" }

// WindowText names the window in the reader's language.
func (r *AdminReturn) WindowText(ctx context.Context) string {
	switch r.Window {
	case "within":
		return i18n.T(ctx, i18n.KeyAdminReturnWindowWithin)
	case "goodwill":
		return i18n.T(ctx, i18n.KeyAdminReturnWindowGoodwill)
	case "after":
		return i18n.T(ctx, i18n.KeyAdminReturnWindowAfter)
	case "undelivered":
		return i18n.T(ctx, i18n.KeyAdminReturnWindowUndelivered)
	case "mixed":
		return i18n.T(ctx, i18n.KeyAdminReturnWindowMixed)
	default:
		panic("pages: unknown rescission window: " + r.Window)
	}
}

// AssessmentVersionText is the hidden input the decide form freezes.
func (r *AdminReturn) AssessmentVersionText() string {
	return strconv.FormatInt(int64(r.AssessmentVersion), 10)
}

// AssessmentStamp is the version and when it was written, for the staff
// member who is about to freeze it.
func (r *AdminReturn) AssessmentStamp(ctx context.Context) string {
	if r.AssessmentVersion == 0 {
		return ""
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRetAssessedAt),
		r.AssessmentVersionText(), r.AssessedAt)
}

// Amount is what approving it would refund.
func (r *AdminReturn) Amount() string { return twd(r.AmountCents) }

// PayoutChannel names the frozen refund sources. Empty until approval, because
// the allocation does not exist until then.
func (r *AdminReturn) PayoutChannel(ctx context.Context) string {
	switch {
	case r.CardRefundCents > 0 && r.CreditRefundCents > 0:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRetPayoutSplit),
			twd(r.CardRefundCents), twd(r.CreditRefundCents))
	case r.CreditRefundCents > 0:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRetPayoutCredit), twd(r.CreditRefundCents))
	case r.CardRefundCents > 0:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRetPayoutCard), twd(r.CardRefundCents))
	default:
		return ""
	}
}

// UnitsText is how many items are being sent back.
func (r *AdminReturn) UnitsText() string { return strconv.FormatInt(int64(r.Units), 10) }

// Action is where a decision on this return posts.
func (r *AdminReturn) Action() string { return "/admin/returns/" + r.ID + "/decide" }

// AssessAction is where a pre-decision eligibility assessment posts.
func (r *AdminReturn) AssessAction() string { return "/admin/returns/" + r.ID + "/assess" }

// InspectAction and CompleteAction are the tail's two forms.
func (r *AdminReturn) InspectAction() string { return "/admin/returns/" + r.ID + "/inspect" }

// CompleteAction closes an inspected return.
func (r *AdminReturn) CompleteAction() string { return "/admin/returns/" + r.ID + "/complete" }

// AdminReturnsView is the return queue.
type AdminReturnsView struct {
	Rows   []AdminReturn
	Notice string
	// Errors keys as "{returnID}.{field}" so a 422 can mark one row without
	// painting every other request on the queue.
	Errors map[string]string
}

// FieldError is the sentence under one control on one request, if any.
func (v AdminReturnsView) FieldError(returnID, field string) string {
	if v.Errors == nil {
		return ""
	}
	return v.Errors[returnID+"."+field]
}

// FieldInvalid reports whether that control should carry aria-invalid.
func (v AdminReturnsView) FieldInvalid(returnID, field string) bool {
	return v.FieldError(returnID, field) != ""
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
	Window      string
	Unused      string
	Packaging   string
	Accessories string
}

// FactValue is the radio this line currently holds. Unknown is the default
// so a first visit cannot look pre-ticked as met.
func (l AdminReturnLine) FactValue(name string) string {
	var got string
	switch name {
	case "unused":
		got = l.Unused
	case "packaging":
		got = l.Packaging
	case "accessories":
		got = l.Accessories
	}
	if got == "" {
		return "unknown"
	}
	return got
}

// FactChecked is whether this radio is the current observation.
func (l AdminReturnLine) FactChecked(name, value string) bool {
	return l.FactValue(name) == value
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
