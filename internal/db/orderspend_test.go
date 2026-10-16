package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

var (
	// netsAnOrdersSpend is the spend rule's arithmetic: an order's total less
	// what went back by card and by store credit.
	netsAnOrdersSpend = regexp.MustCompile(
		`(?i)total_cents\s*-\s*\(\s*(?:\w+\.)?card_cents\s*\+\s*(?:\w+\.)?credit_cents\s*\)`)
	committedOrderSpendView = regexp.MustCompile(`(?is)\bCREATE\s+VIEW\s+committed_order_spend\b[^;]*;`)

	// placesASpendInABand is the tier rule: the bands at or below a spend. The
	// right-hand side is a name or a call, so a CHECK bounding the column by a
	// literal is not one.
	placesASpendInABand = regexp.MustCompile(
		`(?i)\bmin_spend_cents\s*<=\s*[a-z_(]|[a-z_)]\s*>=\s*(?:\w+\.)?min_spend_cents\b`)
	membershipTierForFunction = regexp.MustCompile(
		`(?is)\bCREATE\s+FUNCTION\s+membership_tier_for\b.*?\$\$.*?\$\$;`)

	// asksPerRow is member_tier or member_spend called with a column, once for
	// every row of a set: the membership page did that per account (#1934).
	asksPerRow = regexp.MustCompile(`(?i)\b(member_(?:tier|spend))\s*\(\s*\w+\.\w+`)
)

// TestEveryMemberSpendReadsTheOneView holds committed_order_spend as the one
// definition of what an order counts as spent and membership_tier_for as the
// one placing of a spend in a band, and refuses a per-row call of member_tier
// or member_spend over a set. The spend was written out in member_spend and
// again for the customer page's lifetime figure, and the membership page asked
// member_tier once per account: 0.75 s at ten thousand accounts against 6 ms
// from the view (#1934).
func TestEveryMemberSpendReadsTheOneView(t *testing.T) {
	t.Parallel()

	sources := queryFiles(t)
	migrations, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("no migrations/*.up.sql found; the rules have nowhere to be defined")
	}
	for _, path := range migrations {
		src, readErr := os.ReadFile(path) //nolint:gosec // G304: paths come from the migration glob
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		sources[path] = string(src)
	}
	paths := make([]string, 0, len(sources))
	for path := range sources {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	rules := []struct {
		rule, home string
		pattern    *regexp.Regexp
		within     *regexp.Regexp
		instead    string
		defined    int
	}{
		{rule: "an order's spend", home: "CREATE VIEW committed_order_spend",
			pattern: netsAnOrdersSpend, within: committedOrderSpendView,
			instead: "Read committed_order_spend (or member_order_spend for the membership window)"},
		{rule: "a spend's band", home: "CREATE FUNCTION membership_tier_for",
			pattern: placesASpendInABand, within: membershipTierForFunction,
			instead: "Call membership_tier_for"},
	}
	for _, path := range paths {
		src := maskCalendarSQL(sources[path], true)
		for i := range rules {
			r := &rules[i]
			home := r.within.FindStringIndex(src)
			for _, at := range r.pattern.FindAllStringIndex(src, -1) {
				if len(home) == 2 && at[0] >= home[0] && at[1] <= home[1] {
					r.defined++
					continue
				}
				t.Errorf("%s:%s works out %s itself.\n  %s instead, so a change to the "+
					"rule is made once.", path, migrationLine(src, at[0]), r.rule, r.instead)
			}
		}
		for _, at := range asksPerRow.FindAllStringSubmatchIndex(src, -1) {
			t.Errorf("%s:%s calls %s with a column, once per row.\n  Sum member_order_spend "+
				"for the set and place each spend with membership_tier_for, as "+
				"AdminMembershipTiers does: a per-row call grows with accounts (#1934).",
				path, migrationLine(src, at[0]), src[at[2]:at[3]])
		}
	}
	for _, r := range rules {
		if r.defined != 1 {
			t.Errorf("found %s %d times inside %s, want 1: the definition is gone or no "+
				"longer states it, so this test guards nothing", r.rule, r.defined, r.home)
		}
	}
}
