package db_test

import (
	"regexp"
	"testing"
)

// A balance is the credit LEDGER, added up. Both halves are needed: `amount_cents`
// alone also names refunds and invoice allowances, and the first cut of this guard
// duly refused RefundedSoFar — a sum over a different table entirely. Reading the
// ledger row by row is what /admin/credit's history does and is not a balance;
// adding the amounts up is the act that has to have one definition.
var (
	touchesCreditLedger = regexp.MustCompile(`\bstore_credit_entries\b`)
	sumsAmounts         = regexp.MustCompile(`sum\(\s*-?\s*(\w+\.)?amount_cents\s*\)`)
)

// TestEveryCreditBalanceReadsTheOneView holds the single definition of what an
// account is worth.
//
// Four queries in three packages had each written out `sum(amount_cents)` over
// store_credit_entries — the account page, the checkout, the back office's grant
// form, and the customer page that was being built when this was found. Four
// copies of one arithmetic is four chances for one of them to gain a FILTER the
// others do not have, and a customer shown two different balances by two pages of
// one shop cannot tell which is true.
//
// store_credit_balances is that definition now, the way visible_reviews and
// committed_orders each are, and this is what stops a fifth copy appearing.
func TestEveryCreditBalanceReadsTheOneView(t *testing.T) {
	t.Parallel()

	// Keyed on the QUERY NAME. Each entry is a claim that this query is asking a
	// question the balance view cannot answer — not that it is close enough.
	allowed := map[string]string{
		"OrderCreditPosition": "spent and returned on ONE order, split by sign — a " +
			"position rather than a balance, and the refund split needs both halves",
	}

	found := 0
	for path, src := range queryFiles(t) {
		for _, q := range splitQueries(src) {
			if !touchesCreditLedger.MatchString(q.body) ||
				!sumsAmounts.MatchString(q.body) {
				continue
			}
			found++
			if _, ok := allowed[q.name]; ok {
				continue
			}
			t.Errorf("%s: query %s sums store credit itself.\n"+
				"  Read store_credit_balances instead, or this page can disagree "+
				"with the customer's own account page about what they have. If it "+
				"genuinely asks something the view cannot, name it in the allowlist "+
				"with the reason.", path, q.name)
		}
	}
	if found < len(allowed) {
		t.Fatalf("found %d queries summing the credit ledger and the allowlist names "+
			"%d — the pattern stopped matching", found, len(allowed))
	}
}
