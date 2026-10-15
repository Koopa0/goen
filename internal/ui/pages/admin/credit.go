package admin

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/web"
)

// PostingKind is store_credit_entries.kind: what a posting is, derived by the
// database from its idempotency key. It is not the staff-written reason.
type PostingKind string

const (
	PostingOrderSpend       PostingKind = "order_spend"
	PostingOrderReversal    PostingKind = "order_cancel_reversal"
	PostingReturnPayout     PostingKind = "return_payout"
	PostingPointsRedemption PostingKind = "points_redemption"
	PostingStaffGrant       PostingKind = "staff_grant"
)

type CreditEntry struct {
	Email       string
	AmountCents int64
	Kind        PostingKind
	Reason      string
	At          string
	// CustomerID is empty once the account's owner is erased.
	CustomerID  string
	OrderNumber string
	ReturnID    string
	// ActorEmail is the person who made the posting, empty when none did.
	ActorEmail string
	// RunningTotalCents is the account's total through this posting in ledger
	// order: only the page for one customer shows it.
	RunningTotalCents int64
}

// CreditReason is how a posting reads to the person looking at it: the system
// kinds in the reader's language, and a staff grant as the text staff typed. It
// reads kind and never matches reason, so a grant worded "points" stays a grant.
func CreditReason(ctx context.Context, kind PostingKind, reason string) string {
	switch kind {
	case PostingOrderSpend:
		return i18n.T(ctx, i18n.KeyAdminCreditReasonOrderSpend)
	case PostingOrderReversal:
		return i18n.T(ctx, i18n.KeyAdminCreditReasonOrderReversed)
	case PostingReturnPayout:
		return i18n.T(ctx, i18n.KeyAdminCreditReasonReturnPayout)
	case PostingPointsRedemption:
		return i18n.T(ctx, i18n.KeyAdminCreditReasonPoints)
	case PostingStaffGrant:
		return reason
	default:
		panic("pages: no label for store credit kind " + string(kind))
	}
}

func (e CreditEntry) ReasonText(ctx context.Context) string {
	return CreditReason(ctx, e.Kind, e.Reason)
}

func (e CreditEntry) Amount() string {
	if e.AmountCents < 0 {
		return "-" + money.TWD(-e.AmountCents)
	}
	return "+" + money.TWD(e.AmountCents)
}

func (e CreditEntry) IsSpend() bool { return e.AmountCents < 0 }

func (e CreditEntry) CustomerHref() string { return "/admin/customers/" + e.CustomerID }

// SourceHref is the return a payout settles, else the order a posting belongs
// to, and empty for a posting with neither.
func (e CreditEntry) SourceHref() string {
	switch {
	case e.ReturnID != "":
		return web.ScopeURL("/admin/returns", "request", e.ReturnID)
	case e.OrderNumber != "":
		return "/admin/orders/" + e.OrderNumber
	default:
		return ""
	}
}

func (e CreditEntry) RunningTotal() string { return money.TWD(e.RunningTotalCents) }

type CreditView struct {
	web.Bound

	Rows   []CreditEntry
	Notice components.Result
	Email  string
	Reason string
	Amount string
	// OperationID identifies one rendered grant form across HTTP retries. It is
	// deliberately separate from the per-request log correlation id.
	OperationID   string
	Confirm       bool
	EmailInvalid  bool
	AmountInvalid bool
	ReasonInvalid bool
	GrantCents    int64
	CustomerID    string
	CustomerName  string
	BalanceCents  int64

	// FilterCustomerID narrows the history to one customer; FilterCustomerName is
	// how that page names them.
	FilterCustomerID   string
	FilterCustomerName string
}

func (v *CreditView) Filtered() bool { return v.FilterCustomerID != "" }

func (v *CreditView) Empty() bool { return len(v.Rows) == 0 }

func (e CreditEntry) Who(ctx context.Context) string {
	if e.Email == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedShort)
	}
	return e.Email
}

func (v *CreditView) Balance() string { return money.TWD(v.BalanceCents) }

func (v *CreditView) GrantAmount() string { return money.TWD(v.GrantCents) }
