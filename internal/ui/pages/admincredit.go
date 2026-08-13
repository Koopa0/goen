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

// Amount is the posting, signed: a grant reads positive and a spend negative,
// because a ledger that hides the sign is a list of numbers that do not add up.
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
	Rows   []AdminCreditEntry
	Notice string
	// Email and Reason carry a refused form's values back into it.
	Email  string
	Reason string
	Amount string
}

// Empty reports whether the ledger has nothing in it yet.
func (v AdminCreditView) Empty() bool { return len(v.Rows) == 0 }

// Who is the account the posting went to, or a note that it has been erased.
//
// The fallback used to live in the query's own coalesce(), which is chrome
// written where nobody can ask who is reading.
func (e AdminCreditEntry) Who(ctx context.Context) string {
	if e.Email == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedShort)
	}
	return e.Email
}
