package pages

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
)

// AdminCreditEntry is one posting in the ledger.
type AdminCreditEntry struct {
	Email       string
	AmountCents int64
	Reason      string
	At          string
}

// Amount is the posting, signed: a grant positive and a spend negative.
func (e AdminCreditEntry) Amount() string {
	if e.AmountCents < 0 {
		return "-" + twd(-e.AmountCents)
	}
	return "+" + twd(e.AmountCents)
}

// IsSpend reports whether this posting took credit away.
func (e AdminCreditEntry) IsSpend() bool { return e.AmountCents < 0 }

// AdminCreditView is the store-credit page.
type AdminCreditView struct {
	ListBound

	Rows   []AdminCreditEntry
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
func (v AdminCreditView) Empty() bool { return len(v.Rows) == 0 }

// Who is the account the posting went to, or a note that it has been erased.
func (e AdminCreditEntry) Who(ctx context.Context) string {
	if e.Email == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedShort)
	}
	return e.Email
}

// Balance is the balance read for the confirmation page.
func (v AdminCreditView) Balance() string { return twd(v.BalanceCents) }

// GrantAmount formats the reviewed amount in the shop currency.
func (v AdminCreditView) GrantAmount() string { return twd(v.GrantCents) }
