//go:build integration

package db_test

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestTheStatedSchemaTotalsAreTheRealOnes holds four numbers two documents state
// and the catalogue also knows: where a document states a number the code also
// knows, the two are one fact and something has to hold them together.
func TestTheStatedSchemaTotalsAreTheRealOnes(t *testing.T) {
	ctx := t.Context()

	counts := []struct {
		what  string
		query string
	}{
		{
			what: "CHECK constraints",
			query: `SELECT count(*) FROM pg_constraint
			        WHERE contype = 'c' AND connamespace = 'public'::regnamespace`,
		},
		{
			what: "foreign keys",
			query: `SELECT count(*) FROM pg_constraint
			        WHERE contype = 'f' AND connamespace = 'public'::regnamespace`,
		},
		{
			// Excluding primary keys: a PK index is a consequence of declaring
			// the key, not a rule somebody chose to add.
			what: "unique indexes",
			query: `SELECT count(*) FROM pg_index i
			        JOIN pg_class c ON c.oid = i.indexrelid
			        WHERE i.indisunique AND NOT i.indisprimary
			          AND c.relnamespace = 'public'::regnamespace`,
		},
		{
			// The rule triggers, which is what both documents count: the
			// set_updated_at ones are stated separately because they enforce
			// nothing.
			what: "rule triggers",
			query: `SELECT count(*) FROM pg_trigger t
			        JOIN pg_class c ON c.oid = t.tgrelid
			        JOIN pg_proc p ON p.oid = t.tgfoid
			        WHERE NOT t.tgisinternal AND p.proname <> 'set_updated_at'
			          AND c.relnamespace = 'public'::regnamespace`,
		},
	}

	actual := make(map[string]int, len(counts))
	for _, c := range counts {
		var n int
		if err := pool.QueryRow(ctx, c.query).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", c.what, err)
		}
		if n == 0 {
			t.Fatalf("counted zero %s, so this test compared nothing", c.what)
		}
		actual[c.what] = n
	}

	// Matched as a whole sentence rather than number by number: a bare "241"
	// would match a line number, a port, or the next figure to drift. The two
	// files word it differently ("CHECKs" against "CHECK constraints"), so the
	// pattern admits both.
	stated := regexp.MustCompile(
		"(\\d+) `?CHECK`?s?(?: constraints)?, (\\d+) foreign keys, " +
			"(\\d+) unique indexes,? and (\\d+) rule triggers")

	// README.md is the one document a clone carries that states these totals.
	body, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	// Newlines folded first: where the sentence wraps is a formatting decision
	// this guard must not depend on.
	flat := strings.Join(strings.Fields(string(body)), " ")
	m := stated.FindStringSubmatch(flat)
	if m == nil {
		t.Fatal("README.md no longer states the four schema totals in the form this " +
			"guard reads; restate them or update the pattern")
	}
	for i, c := range counts {
		claimed, convErr := strconv.Atoi(m[i+1])
		if convErr != nil {
			t.Fatalf("README.md: %v", convErr)
		}
		if claimed != actual[c.what] {
			t.Errorf("README.md says %d %s; the schema has %d", claimed, c.what, actual[c.what])
		}
	}
}
