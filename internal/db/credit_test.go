package db_test

import (
	"regexp"
	"testing"
)

var (
	touchesCreditLedger = regexp.MustCompile(`\bstore_credit_entries\b`)
	sumsAmounts         = regexp.MustCompile(`sum\(\s*-?\s*(\w+\.)?amount_cents\s*\)`)
)

// TestEveryCreditBalanceReadsTheOneView holds store_credit_balances as the one definition of a balance.
func TestEveryCreditBalanceReadsTheOneView(t *testing.T) {
	t.Parallel()

	allowed := map[string]string{
		"OrderCreditPositionExcluding": "spent and returned on ONE order, split by " +
			"sign — a position rather than a balance, and the refund split needs both " +
			"halves. It leaves out one return's own compensation because that is what " +
			"a RETRY has to ask: without it a split return reads the credit it just " +
			"posted as credit already returned and refuses its own resume",
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
	for name, why := range allowed {
		if !used[name] {
			t.Errorf("the allowlist names %s (%s) and nothing matched it: the "+
				"query is gone or renamed, so the entry now excuses nothing. Its presence reads as coverage.", name, why)
		}
	}
}
