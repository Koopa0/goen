package returns

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/order/returns"
	"github.com/koopa0/goen/internal/payment/refund"
	"github.com/koopa0/goen/internal/ui/widget"
)

type Return struct {
	Lines       []Line
	ID          string
	OrderNumber string
	Status      returns.Status
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
	// Window is counted from the request clock against each line.
	Window            returns.PolicyWindow
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
	// PayoutMoneySettled is a payout whose every source has gone back. With
	// PayoutOutstanding it means only the refunded event or the points clawback
	// is left.
	PayoutMoneySettled bool
	// PayoutBlocked says the durable payout facts do not fit their frozen source
	// allocation. Known terminal provider attempts are not blocked: retry appends
	// a new generation with a fresh idempotency key.
	PayoutBlocked bool
	// BeforeShipment is a paid order refunded before anything shipped: nothing
	// comes back to inspect, and its order page finishes it.
	BeforeShipment bool
	// Unsent is a refund of units a partly shipped order will not send:
	// nothing comes back to inspect either, and its order page settles it.
	Unsent bool
}

// CanRetryPayout reports whether the approved decision has money left behind a
// resume-safe door.
func (r *Return) CanRetryPayout() bool {
	return r.Decided && r.PayoutOutstanding && !r.PayoutBlocked
}

// PayoutHint is the sentence beside the retry button: the money is still owed,
// or it has gone back and only the closing step is.
func (r *Return) PayoutHint() i18n.Key {
	if r.PayoutMoneySettled {
		return i18n.KeyAdminRetPayoutFollowUp
	}
	return i18n.KeyAdminRetPayoutOutstanding
}

// PayoutStranded reports whether a person must repair inconsistent durable
// payout facts before goen can safely retry.
func (r *Return) PayoutStranded() bool {
	return r.Decided && r.PayoutOutstanding && r.PayoutBlocked
}

func (r *Return) AwaitingGoods() bool {
	if r.Status != returns.StatusApproved || r.BeforeShipment || r.Unsent {
		return false
	}
	for i := range r.Lines {
		if !r.Lines[i].Inspected {
			return true
		}
	}
	return false
}

// standing is a return's status as the queue shows it: the word and the colour
// group from one switch, so they cannot disagree. An empty key means the word the
// store gave StatusText. An open request or an approval the staff can close is
// the staff's turn; a refund to send again is a payout the approval started and
// did not finish, so it is warn, and a payout that must be repaired is an error;
// an approval still coming back is in progress; a refund before shipment is a
// cancellation, grey like a cancelled order.
func (r *Return) standing() (i18n.Key, widget.Intent) {
	switch r.Status {
	case returns.StatusRequested:
		return "", widget.IntentStaffTurn
	case returns.StatusApproved:
		switch {
		case r.PayoutStranded():
			return i18n.KeyAdminReturnRefundFailed, widget.IntentDanger
		case r.CanRetryPayout():
			return i18n.KeyAdminReturnRefundToResend, widget.IntentWarn
		case r.CanComplete():
			return i18n.KeyAdminReturnReadyToClose, widget.IntentStaffTurn
		default:
			return i18n.KeyAdminReturnOnItsWay, widget.IntentProgress
		}
	case returns.StatusCompleted:
		if r.BeforeShipment || r.Unsent {
			return "", widget.IntentNeutral
		}
		return "", widget.IntentDone
	default:
		return "", widget.IntentNeutral
	}
}

func (r *Return) StatusIntent() widget.Intent {
	_, intent := r.standing()
	return intent
}

func (r *Return) StatusLabel(ctx context.Context) string {
	if key, _ := r.standing(); key != "" {
		return i18n.T(ctx, key)
	}
	return r.StatusText
}

func (r *Return) CanComplete() bool {
	if r.Status != returns.StatusApproved || len(r.Lines) == 0 || r.BeforeShipment {
		return false
	}
	for i := range r.Lines {
		if !r.Lines[i].Inspected {
			return false
		}
	}
	return true
}

func (r *Return) RestockedUnits() int32 {
	var n int32
	for i := range r.Lines {
		n += r.Lines[i].Restocked
	}
	return n
}

func (r *Return) RestockedUnitsText() string { return strconv.FormatInt(int64(r.RestockedUnits()), 10) }

func (r *Return) Rescission() bool { return r.Window == returns.WindowStatutory }

// CanReject excludes requests whose undelivered lines have no recorded start
// for the statutory clock, including requests with other line windows.
func (r *Return) CanReject() bool {
	if r.Rescission() || r.Window == returns.WindowUndelivered {
		return false
	}
	for i := range r.Lines {
		if r.Lines[i].Window == returns.WindowUndelivered {
			return false
		}
	}
	return true
}

// Goodwill reports whether this request is inside the shop's advertised
// days 8–14. Entitlement still depends on unused-and-complete facts.
func (r *Return) Goodwill() bool { return r.Window == returns.WindowGoodwill }

func (r *Return) Late() bool { return r.Window == returns.WindowLate }

func (r *Return) Mixed() bool { return r.Window == returns.WindowMixed }

func (r *Return) mixedStatutoryApproval() bool {
	if !r.Mixed() || len(r.Lines) == 0 {
		return false
	}
	for i := range r.Lines {
		switch r.Lines[i].Window {
		case returns.WindowStatutory, returns.WindowUndelivered:
		default:
			return false
		}
	}
	return true
}

func (r *Return) AssessmentVersionText() string {
	return strconv.FormatInt(int64(r.AssessmentVersion), 10)
}

func (r *Return) AssessmentStamp(ctx context.Context) string {
	if r.AssessmentVersion == 0 {
		return ""
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRetAssessedAt),
		r.AssessmentVersionText(), r.AssessedAt)
}

func (r *Return) Amount() string { return money.TWD(r.AmountCents) }

// PayoutChannel names the frozen refund sources. Empty until approval, because
// the allocation does not exist until then.
func (r *Return) PayoutChannel(ctx context.Context) string {
	return refund.PayoutChannel(ctx, r.CardRefundCents, r.CreditRefundCents)
}

func (r *Return) UnitsText() string { return strconv.FormatInt(int64(r.Units), 10) }

func (r *Return) Action() string { return "/admin/returns/" + r.ID + "/decide" }

// OrderAction is the order page: where a refund before shipment resumes, and
// where the shipment and delivery date of any return are read.
func (r *Return) OrderAction() string { return "/admin/orders/" + r.OrderNumber }

func (r *Return) AssessAction() string { return "/admin/returns/" + r.ID + "/assess" }

func (r *Return) InspectAction() string { return "/admin/returns/" + r.ID + "/inspect" }

func (r *Return) CompleteAction() string { return "/admin/returns/" + r.ID + "/complete" }

type View struct {
	keyset.Bound

	Rows   []Return
	Notice widget.Result
	// OwnPage is a page for one return, /admin/returns?request={id}: its forms
	// answer back to it instead of to the queue.
	OwnPage bool
	// Errors keys as "{returnID}.{field}" so a 422 can mark one row without
	// painting every other request on the queue.
	Errors map[string]string
}

func (v View) FieldRefusal(returnID, field string) string {
	if v.Errors == nil {
		return ""
	}
	return v.Errors[returnID+"."+field]
}

func (v View) FieldInvalid(returnID, field string) bool {
	return v.FieldRefusal(returnID, field) != ""
}

func (v View) Empty() bool { return len(v.Rows) == 0 }

type Line struct {
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
	Window      returns.PolicyWindow
	Unused      string
	Packaging   string
	Accessories string
	// DraftReceived, DraftRestocked and DraftNote are what staff typed on an
	// inspection that was refused, including empty submitted counts.
	DraftReceived, DraftRestocked, DraftNote string
}

func (l *Line) ReceivedField(refused bool) string {
	if refused || l.DraftReceived != "" {
		return l.DraftReceived
	}
	return l.MaxQuantityText()
}

func (l *Line) RestockedField(refused bool) string {
	if refused || l.DraftRestocked != "" {
		return l.DraftRestocked
	}
	return "0"
}

// FactValue is the radio this line currently holds. Unknown is the default
// so a first visit cannot look pre-ticked as met.
func (l *Line) FactValue(name string) string {
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

func (l *Line) FactChecked(name, value string) bool {
	return l.FactValue(name) == value
}

func (l *Line) ReceivedText() string {
	return strconv.FormatInt(int64(l.Received), 10)
}

func (l *Line) RestockedText() string {
	return strconv.FormatInt(int64(l.Restocked), 10)
}

func (l *Line) MaxQuantityText() string {
	return strconv.FormatInt(int64(l.Quantity), 10)
}

func (l *Line) Shortfall() bool { return l.Inspected && l.Received < l.Quantity }

func (l *Line) Scrapped() bool { return l.Inspected && l.Restocked < l.Received }

func (l *Line) Line() string {
	name := l.Name
	if l.Label != "" {
		name += " · " + l.Label
	}
	return name + " × " + strconv.FormatInt(int64(l.Quantity), 10)
}

func (l *Line) UnitPrice() string { return money.TWD(l.UnitCents) }

type Confirmation struct {
	ID                string
	OrderNumber       string
	Decision          string
	Reason            string
	AmountCents       int64
	Resolution        string
	AssessmentVersion string
	Required          bool
	Retry             bool
	// OwnPage is a decision asked from the return's own page, which Back and the
	// final post return to.
	OwnPage bool
}

func (v Confirmation) Title(ctx context.Context) string {
	switch v.Decision {
	case "rejected":
		return i18n.T(ctx, i18n.KeyAdminRetConfirmReject)
	case "exception":
		return i18n.T(ctx, i18n.KeyAdminRetConfirmException)
	default:
		if v.Retry {
			return i18n.T(ctx, i18n.KeyAdminRetConfirmRetry)
		}
		return i18n.T(ctx, i18n.KeyAdminRetConfirmApprove)
	}
}

func (v Confirmation) Amount() string { return money.TWD(v.AmountCents) }

func (v Confirmation) Action() string { return "/admin/returns/" + v.ID + "/decide" }

func (v Confirmation) Back() string {
	if v.OwnPage {
		return keyset.ScopeURL("/admin/returns", "request", v.ID)
	}
	return "/admin/returns"
}
