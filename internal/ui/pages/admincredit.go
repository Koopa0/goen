package pages

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
