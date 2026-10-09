package admin

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/web"
)

type CreditEntry struct {
	Email        string
	AmountCents  int64
	Reason       string
	At           string
	CustomerID   string
	OrderNumber  string
	ReturnID     string
	BalanceCents int64
	ActorName    string
}

// The reasons the database writes itself, in the words migrations/001 stores
// them: spend_store_credit, the reversal of a spend, the return payout and a
// points redemption. Any other reason is a grant's own text and is shown as typed.
const (
	reasonOrderSpend    = "訂單折抵" // i18n-exempt: the stored reason, matched exactly.
	reasonOrderReversed = "order cancelled"
	reasonReturnPayout  = "退貨退回購物金" // i18n-exempt: the stored reason, matched exactly.
	reasonPoints        = "points"
)

// CreditReason is a store-credit reason in the reader's language where goen
// wrote it, and as typed where staff did.
func CreditReason(ctx context.Context, reason string) string {
	switch reason {
	case reasonOrderSpend:
		return i18n.T(ctx, i18n.KeyAdminCreditReasonOrderSpend)
	case reasonOrderReversed:
		return i18n.T(ctx, i18n.KeyAdminCreditReasonOrderReversed)
	case reasonReturnPayout:
		return i18n.T(ctx, i18n.KeyAdminCreditReasonReturnPayout)
	case reasonPoints:
		return i18n.T(ctx, i18n.KeyAdminCreditReasonPoints)
	default:
		return reason
	}
}

func (e CreditEntry) ReasonText(ctx context.Context) string { return CreditReason(ctx, e.Reason) }

func (e CreditEntry) Amount() string {
	if e.AmountCents < 0 {
		return "-" + money.TWD(-e.AmountCents)
	}
	return "+" + money.TWD(e.AmountCents)
}

func (e CreditEntry) IsSpend() bool { return e.AmountCents < 0 }

func (e CreditEntry) CustomerHref() string { return "/admin/customers/" + e.CustomerID }

func (e CreditEntry) SourceHref() string {
	if e.ReturnID != "" {
		return web.ScopeURL("/admin/returns", "request", e.ReturnID)
	}
	if e.OrderNumber != "" {
		return "/admin/orders/" + e.OrderNumber
	}
	return ""
}

func (e CreditEntry) Balance() string { return money.TWD(e.BalanceCents) }

type CreditView struct {
	web.Bound

	Rows   []CreditEntry
	Notice components.Result
	Email  string
	Reason string
	Amount string
	// OperationID identifies one rendered grant form across HTTP retries. It is
	// deliberately separate from the per-request log correlation id.
	OperationID        string
	Confirm            bool
	EmailInvalid       bool
	AmountInvalid      bool
	ReasonInvalid      bool
	GrantCents         int64
	CustomerID         string
	CustomerName       string
	BalanceCents       int64
	FilterCustomerID   string
	FilterCustomerName string
}

func (v *CreditView) Empty() bool { return len(v.Rows) == 0 }

func (e CreditEntry) Who(ctx context.Context) string {
	if e.Email == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedShort)
	}
	return e.Email
}

func (v *CreditView) Balance() string { return money.TWD(v.BalanceCents) }

func (v *CreditView) GrantAmount() string { return money.TWD(v.GrantCents) }
