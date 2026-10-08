package admin

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/web"
)

type Return struct {
	Lines       []ReturnLine
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
	// PayoutBlocked says the durable payout facts do not fit their frozen source
	// allocation. Known terminal provider attempts are not blocked: retry appends
	// a new generation with a fresh idempotency key.
	PayoutBlocked bool
	// BeforeShipment is a paid order refunded before anything shipped: nothing
	// comes back to inspect, and its order page finishes it.
	BeforeShipment bool
}

// CanRetryPayout reports whether the approved decision has money left behind a
// resume-safe door.
func (r *Return) CanRetryPayout() bool {
	return r.Decided && r.PayoutOutstanding && !r.PayoutBlocked
}

// PayoutStranded reports whether a person must repair inconsistent durable
// payout facts before goen can safely retry.
func (r *Return) PayoutStranded() bool {
	return r.Decided && r.PayoutOutstanding && r.PayoutBlocked
}

func (r *Return) AwaitingGoods() bool {
	if r.Status != returns.StatusApproved || r.BeforeShipment {
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
// store gave StatusText. An open request, an approval the staff can close or whose
// money is owed needs the staff; a payout that must be repaired is an error; an
// approval still coming back is in progress; a refund before shipment is a
// cancellation, grey like a cancelled order.
func (r *Return) standing() (i18n.Key, components.Intent) {
	switch r.Status {
	case returns.StatusRequested:
		return "", components.IntentWarn
	case returns.StatusApproved:
		switch {
		case r.PayoutStranded():
			return i18n.KeyAdminReturnRefundFailed, components.IntentDanger
		case r.CanRetryPayout():
			return i18n.KeyAdminReturnRefundToResend, components.IntentWarn
		case r.CanComplete():
			return i18n.KeyAdminReturnReadyToClose, components.IntentWarn
		default:
			return i18n.KeyAdminReturnOnItsWay, components.IntentProgress
		}
	case returns.StatusCompleted:
		if r.BeforeShipment {
			return "", components.IntentNeutral
		}
		return "", components.IntentDone
	default:
		return "", components.IntentNeutral
	}
}

func (r *Return) StatusIntent() components.Intent {
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

func (r *Return) RestockedUnitsText() string {
	var n int32
	for i := range r.Lines {
		n += r.Lines[i].Restocked
	}
	return strconv.FormatInt(int64(n), 10)
}

func ReturnLineWindowText(ctx context.Context, window returns.PolicyWindow) string {
	return (&Return{Window: window}).WindowText(ctx)
}

func (r *Return) Rescission() bool { return r.Window == returns.WindowStatutory }

// Goodwill reports whether this request is inside the shop's advertised
// days 8–14. Entitlement still depends on unused-and-complete facts.
func (r *Return) Goodwill() bool { return r.Window == returns.WindowGoodwill }

func (r *Return) Late() bool { return r.Window == returns.WindowLate }

func (r *Return) Mixed() bool { return r.Window == returns.WindowMixed }

func (r *Return) WindowText(ctx context.Context) string {
	switch r.Window {
	case returns.WindowStatutory:
		return i18n.T(ctx, i18n.KeyAdminReturnWindowWithin)
	case returns.WindowGoodwill:
		return i18n.T(ctx, i18n.KeyAdminReturnWindowGoodwill)
	case returns.WindowLate:
		return i18n.T(ctx, i18n.KeyAdminReturnWindowAfter)
	case returns.WindowUndelivered:
		return i18n.T(ctx, i18n.KeyAdminReturnWindowUndelivered)
	case returns.WindowMixed:
		return i18n.T(ctx, i18n.KeyAdminReturnWindowMixed)
	default:
		panic("pages: unknown rescission window: " + string(r.Window))
	}
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
	switch {
	case r.CardRefundCents > 0 && r.CreditRefundCents > 0:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRetPayoutSplit),
			money.TWD(r.CardRefundCents), money.TWD(r.CreditRefundCents))
	case r.CreditRefundCents > 0:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRetPayoutCredit), money.TWD(r.CreditRefundCents))
	case r.CardRefundCents > 0:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRetPayoutCard), money.TWD(r.CardRefundCents))
	default:
		return ""
	}
}

func (r *Return) UnitsText() string { return strconv.FormatInt(int64(r.Units), 10) }

func (r *Return) Action() string { return "/admin/returns/" + r.ID + "/decide" }

// OrderAction is the order page: where a refund before shipment resumes, and
// where the shipment and delivery date of any return are read.
func (r *Return) OrderAction() string { return "/admin/orders/" + r.OrderNumber }

func (r *Return) AssessAction() string { return "/admin/returns/" + r.ID + "/assess" }

func (r *Return) InspectAction() string { return "/admin/returns/" + r.ID + "/inspect" }

func (r *Return) CompleteAction() string { return "/admin/returns/" + r.ID + "/complete" }

type ReturnsView struct {
	web.Bound

	Rows   []Return
	Notice components.Result
	// Errors keys as "{returnID}.{field}" so a 422 can mark one row without
	// painting every other request on the queue.
	Errors map[string]string
}

func (v ReturnsView) FieldRefusal(returnID, field string) string {
	if v.Errors == nil {
		return ""
	}
	return v.Errors[returnID+"."+field]
}

func (v ReturnsView) FieldInvalid(returnID, field string) bool {
	return v.FieldRefusal(returnID, field) != ""
}

func (v ReturnsView) Empty() bool { return len(v.Rows) == 0 }

type ReturnLine struct {
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
	// inspection that was refused; empty means the form's own defaults.
	DraftReceived, DraftRestocked, DraftNote string
}

func (l *ReturnLine) ReceivedField() string {
	if l.DraftReceived != "" {
		return l.DraftReceived
	}
	return l.MaxQuantityText()
}

func (l *ReturnLine) RestockedField() string {
	if l.DraftRestocked != "" {
		return l.DraftRestocked
	}
	return "0"
}

// FactValue is the radio this line currently holds. Unknown is the default
// so a first visit cannot look pre-ticked as met.
func (l *ReturnLine) FactValue(name string) string {
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

func (l *ReturnLine) FactChecked(name, value string) bool {
	return l.FactValue(name) == value
}

func (l *ReturnLine) ReceivedText() string {
	return strconv.FormatInt(int64(l.Received), 10)
}

func (l *ReturnLine) RestockedText() string {
	return strconv.FormatInt(int64(l.Restocked), 10)
}

func (l *ReturnLine) MaxQuantityText() string {
	return strconv.FormatInt(int64(l.Quantity), 10)
}

func (l *ReturnLine) Shortfall() bool { return l.Inspected && l.Received < l.Quantity }

func (l *ReturnLine) Scrapped() bool { return l.Inspected && l.Restocked < l.Received }

func (l *ReturnLine) Line() string {
	name := l.Name
	if l.Label != "" {
		name += " · " + l.Label
	}
	return name + " × " + strconv.FormatInt(int64(l.Quantity), 10)
}

func (l *ReturnLine) UnitPrice() string { return money.TWD(l.UnitCents) }

type ReturnConfirmation struct {
	ID                string
	OrderNumber       string
	Decision          string
	Reason            string
	AmountCents       int64
	Resolution        string
	AssessmentVersion string
	Required          bool
	Retry             bool
}

func (v ReturnConfirmation) Title(ctx context.Context) string {
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

func (v ReturnConfirmation) Amount() string { return money.TWD(v.AmountCents) }

func (v ReturnConfirmation) Action() string { return "/admin/returns/" + v.ID + "/decide" }

// RefundConfirmation is a refund before shipment, before it moves money.
// Resume means the refund is already open, its split frozen and its reason
// recorded. CreditPaid is a pending order store credit alone paid: confirming
// cancels it at once and returns the credit, as its customer's own
// cancellation would.
type RefundConfirmation struct {
	OrderNumber   string
	TotalCents    int64
	CardCents     int64
	CreditCents   int64
	Resume        bool
	CreditPaid    bool
	Reason        string
	ReasonInvalid bool
}

func (v RefundConfirmation) Amount() string { return money.TWD(v.TotalCents) }

// Total is the amount the confirming POST repeats; a different figure is a
// refund the staff member was not shown.
func (v RefundConfirmation) Total() string { return strconv.FormatInt(v.TotalCents, 10) }

// CreditCancellation is what confirming does to an order store credit alone
// paid.
func (v RefundConfirmation) CreditCancellation(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRefundCreditReturn), money.TWD(v.CreditCents))
}

func (v RefundConfirmation) Channel(ctx context.Context) string {
	return (&Return{CardRefundCents: v.CardCents, CreditRefundCents: v.CreditCents}).PayoutChannel(ctx)
}

func (v RefundConfirmation) Action() string { return "/admin/orders/" + v.OrderNumber + "/refund" }

func (v RefundConfirmation) Back() string { return "/admin/orders/" + v.OrderNumber }
