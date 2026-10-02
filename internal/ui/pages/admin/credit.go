package admin

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/pages"
)

// CreditEntry is one posting in the ledger.
type CreditEntry struct {
	Email       string
	AmountCents int64
	Reason      string
	At          string
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

// ReasonText is the posting's reason, as CreditReason words it.
func (e CreditEntry) ReasonText(ctx context.Context) string { return CreditReason(ctx, e.Reason) }

// Amount is the posting, signed: a grant positive and a spend negative.
func (e CreditEntry) Amount() string {
	if e.AmountCents < 0 {
		return "-" + money.TWD(-e.AmountCents)
	}
	return "+" + money.TWD(e.AmountCents)
}

// IsSpend reports whether this posting took credit away.
func (e CreditEntry) IsSpend() bool { return e.AmountCents < 0 }

// CreditView is the store-credit page.
type CreditView struct {
	pages.ListBound

	Rows   []CreditEntry
	Notice string
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
}

// Empty reports whether the ledger has nothing in it yet.
func (v *CreditView) Empty() bool { return len(v.Rows) == 0 }

// Who is the account the posting went to, or a note that it has been erased.
func (e CreditEntry) Who(ctx context.Context) string {
	if e.Email == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedShort)
	}
	return e.Email
}

// Balance is the balance read for the confirmation page.
func (v *CreditView) Balance() string { return money.TWD(v.BalanceCents) }

// GrantAmount formats the reviewed amount in the shop currency.
func (v *CreditView) GrantAmount() string { return money.TWD(v.GrantCents) }
