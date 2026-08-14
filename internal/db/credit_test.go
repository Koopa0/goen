package db_test

import (
	"regexp"
	"testing"
)

// A balance is the credit LEDGER, added up. Both halves are needed, and the ledger
// half is the one that is easy to leave out: `amount_cents` alone also names refunds
// and invoice allowances, so a guard anchored on the arithmetic alone refuses
// RefundedSoFar — a sum over a different table entirely. Reading the ledger row by
// row is what /admin/credit's history does and is not a balance; adding the amounts
// up is the act that has to have one definition.
var (
	touchesCreditLedger = regexp.MustCompile(`\bstore_credit_entries\b`)
	sumsAmounts         = regexp.MustCompile(`sum\(\s*-?\s*(\w+\.)?amount_cents\s*\)`)
)

// TestEveryCreditBalanceReadsTheOneView holds the single definition of what an
// account is worth.
//
// Four surfaces ask what an account is worth — the account page, the checkout, the
// back office's grant form and the customer page — and each of them could write out
// its own `sum(amount_cents)` over store_credit_entries. Four copies of one
// arithmetic is four chances for one to gain a FILTER the others do not have, and a
// customer shown two different balances by two pages of one shop cannot tell which
// is true.
//
// store_credit_balances is that one definition, the way visible_reviews and
// committed_orders each are, and this is what stops a fifth copy appearing.
func TestEveryCreditBalanceReadsTheOneView(t *testing.T) {
	t.Parallel()

	// Keyed on the QUERY NAME. Each entry is a claim that this query is asking a
	// question the balance view cannot answer — not that it is close enough.
	allowed := map[string]string{
		"OrderCreditPosition": "spent and returned on ONE order, split by sign — a " +
			"position rather than a balance, and the refund split needs both halves",
	}

	used := map[string]bool{}
	for path, src := range queryFiles(t) {
		for _, q := range splitQueries(src) {
			if !touchesCreditLedger.MatchString(q.body) ||
				!sumsAmounts.MatchString(q.body) {
				continue
			}
			if _, ok := allowed[q.name]; ok {
				used[q.name] = true
				continue
			}
			t.Errorf("%s: query %s sums store credit itself.\n"+
				"  Read store_credit_balances instead, or this page can disagree "+
				"with the customer's own account page about what they have. If it "+
				"genuinely asks something the view cannot, name it in the allowlist "+
				"with the reason.", path, q.name)
		}
	}
	// Checked by IDENTITY, not by count. `found < len(allowed)` fires on a
	// stale entry but cannot say WHICH, so it reports "the pattern stopped
	// matching" — a message that sends the reader to the regex when the
	// defect is an allowlist row naming a query that no longer exists.
	for name, why := range allowed {
		if !used[name] {
			t.Errorf("the allowlist names %s (%s) and nothing matched it: the "+
				"query is gone or renamed, so the entry now excuses nothing. Its presence reads as coverage.", name, why)
		}
	}
}
