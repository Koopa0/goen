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
