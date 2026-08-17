package db_test

import (
	"regexp"
	"testing"
)

// Both halves matter: amount_cents alone also names refunds and invoice allowances.
var (
	touchesCreditLedger = regexp.MustCompile(`\bstore_credit_entries\b`)
	sumsAmounts         = regexp.MustCompile(`sum\(\s*-?\s*(\w+\.)?amount_cents\s*\)`)
)

// TestEveryCreditBalanceReadsTheOneView holds store_credit_balances as the single
// definition of what an account is worth.
func TestEveryCreditBalanceReadsTheOneView(t *testing.T) {
	t.Parallel()

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
	// Checked by identity, not by count: a count cannot name the stale entry.
	for name, why := range allowed {
		if !used[name] {
			t.Errorf("the allowlist names %s (%s) and nothing matched it: the "+
				"query is gone or renamed, so the entry now excuses nothing. Its presence reads as coverage.", name, why)
		}
	}
}
