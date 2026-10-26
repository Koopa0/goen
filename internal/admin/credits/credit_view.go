package credits

import (
	"context"

	"github.com/koopa0/goen/internal/credit"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/widget"
)

type Entry struct {
	Email       string
	AmountCents int64
	Kind        credit.Kind
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

func (e Entry) ReasonText(ctx context.Context) string {
	return credit.Reason(ctx, e.Kind, e.Reason)
}

func (e Entry) Amount() string {
	if e.AmountCents < 0 {
		return "-" + money.TWD(-e.AmountCents)
	}
	return "+" + money.TWD(e.AmountCents)
}

func (e Entry) IsSpend() bool { return e.AmountCents < 0 }

func (e Entry) CustomerHref() string { return "/admin/customers/" + e.CustomerID }

// SourceHref is the return a payout settles, else the order a posting belongs
// to, and empty for a posting with neither.
func (e Entry) SourceHref() string {
	switch {
	case e.ReturnID != "":
		return keyset.ScopeURL("/admin/returns", "request", e.ReturnID)
	case e.OrderNumber != "":
		return "/admin/orders/" + e.OrderNumber
	default:
		return ""
	}
}

func (e Entry) RunningTotal() string { return money.TWD(e.RunningTotalCents) }

type View struct {
	keyset.Bound

	Rows   []Entry
	Notice widget.Result
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

func (v *View) Filtered() bool { return v.FilterCustomerID != "" }

func (v *View) Empty() bool { return len(v.Rows) == 0 }

func (e Entry) Who(ctx context.Context) string {
	if e.Email == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedShort)
	}
	return e.Email
}

func (v *View) Balance() string { return money.TWD(v.BalanceCents) }

func (v *View) GrantAmount() string { return money.TWD(v.GrantCents) }
