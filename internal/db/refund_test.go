package db_test

import (
	"regexp"
	"testing"
)

var (
	sumsCardRefunds = regexp.MustCompile(
		`(?is)\bsum\(\s*(?:\w+\.)?amount_cents\s*\)` +
			`\s*(?:,\s*0\s*\)\s*::bigint)?\s+from\s+refunds\b`,
	)
	sumsPositiveOrderCredit = regexp.MustCompile(
		`(?is)\bsum\(\s*(?:\w+\.)?amount_cents\s*\)\s+from\s+store_credit_entries\b.*` +
			`\bwhere\b.*(?:\w+\.)?order_id\s+is\s+not\s+null.*` +
			`(?:\w+\.)?amount_cents\s*>\s*0`,
	)
)

// TestEveryRefundTotalReadsTheOneView holds order_refunds as the one
// per-order definition of what went back by card and store credit.
func TestEveryRefundTotalReadsTheOneView(t *testing.T) {
	t.Parallel()

	allowed := map[string]string{
		"RefundedSoFar": "a per-payment position used before opening a refund; it " +
			"includes outstanding provider states and excludes this request_key so a " +
			"retry does not refuse its own claim, which order_refunds cannot express",
		"RevenueSince": "a time window rather than a per-order total; its succeeded " +
			"card and positive order-credit predicates must remain identical to " +
			"order_refunds before the two sources are windowed",
	}

	used := map[string]bool{}
	for path, src := range queryFiles(t) {
		for _, q := range splitQueries(src) {
			if !sumsCardRefunds.MatchString(q.body) &&
				!sumsPositiveOrderCredit.MatchString(q.body) {
				continue
			}
			if _, ok := allowed[q.name]; ok {
				used[q.name] = true
				continue
			}
			t.Errorf("%s: query %s computes a refund total outside order_refunds.\n"+
				"  Read order_refunds instead, or the timeline, report, invoice and "+
				"allowance form can disagree about what went back. If this query asks "+
				"a per-payment or time-window question the view cannot answer, name it "+
				"in the allowlist with that exact reason.", path, q.name)
		}
	}
	for name, why := range allowed {
		if !used[name] {
			t.Errorf("the allowlist names %s (%s) and nothing matched it: the query "+
				"is gone or renamed, so the entry now excuses nothing. Its presence "+
				"reads as coverage.", name, why)
		}
	}
}
