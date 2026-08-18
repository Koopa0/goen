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
// and the catalogue also knows.
//
// CLAUDE.md's own account of why they are worth stating says the counts "had
// drifted to roughly half" when they were kept by hand — and then both files
// went on keeping them by hand. They had drifted again by the time this was
// written: 230 CHECKs against 241, 15 updated_at triggers against 16, 18
// SECURITY DEFINER functions against 19. Nothing was wrong with the schema; the
// prose describing it had simply stopped being true, in the two files a reader
// consults first.
//
// This is TestTheStatedHoldMatchesTheEnforcedOne's shape — a page-says-versus-
// code-does guard — applied to the other set of numbers this repository states.
// The rule it enforces is the one already written down: where a document states
// a number the code also knows, the two are one fact and something has to hold
// them together.
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

	// The sentence both files carry, read for its four figures at once. Matching
	// the whole sentence rather than each number alone is deliberate: a bare
	// "241" would match a line number, a port, or the next figure to drift.
	// The two files word it differently ("CHECKs" against "CHECK constraints"),
	// so the pattern admits both rather than forcing one file's prose.
	stated := regexp.MustCompile(
		"(\\d+) `?CHECK`?s?(?: constraints)?, (\\d+) foreign keys, " +
			"(\\d+) unique indexes,? and (\\d+) rule triggers")

	for _, file := range []string{"../../README.md", "../../CLAUDE.md"} {
		body, err := os.ReadFile(file) //nolint:gosec // G304: two fixed paths in this repository
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		// Newlines folded first: both sentences wrap, and where they wrap is a
		// formatting decision this guard must not depend on.
		flat := strings.Join(strings.Fields(string(body)), " ")
		m := stated.FindStringSubmatch(flat)
		if m == nil {
			t.Errorf("%s no longer states the four schema totals in the form this "+
				"guard reads; restate them or update the pattern", file)
			continue
		}
		for i, c := range counts {
			claimed, convErr := strconv.Atoi(m[i+1])
			if convErr != nil {
				t.Fatalf("%s: %v", file, convErr)
			}
			if claimed != actual[c.what] {
				t.Errorf("%s says %d %s; the schema has %d",
					file, claimed, c.what, actual[c.what])
			}
		}
	}

	// The two figures that sit apart from that sentence, each in one file only.
	apart := []struct {
		file, pattern, what, query string
	}{
		{
			file: "../../README.md", pattern: `beside (\d+) that only keep .updated_at. truthful`,
			what: "set_updated_at triggers",
			query: `SELECT count(*) FROM pg_trigger t
			        JOIN pg_class c ON c.oid = t.tgrelid
			        JOIN pg_proc p ON p.oid = t.tgfoid
			        WHERE NOT t.tgisinternal AND p.proname = 'set_updated_at'
			          AND c.relnamespace = 'public'::regnamespace`,
		},
		{
			file: "../../README.md", pattern: `through (\d+) .SECURITY DEFINER. functions`,
			what: "SECURITY DEFINER functions",
			query: `SELECT count(*) FROM pg_proc
			        WHERE prosecdef AND pronamespace = 'public'::regnamespace`,
		},
	}
	for _, a := range apart {
		var n int
		if err := pool.QueryRow(ctx, a.query).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", a.what, err)
		}
		body, err := os.ReadFile(a.file)
		if err != nil {
			t.Fatalf("read %s: %v", a.file, err)
		}
		flat := strings.Join(strings.Fields(string(body)), " ")
		m := regexp.MustCompile(a.pattern).FindStringSubmatch(flat)
		if m == nil {
			t.Errorf("%s no longer states the %s where this guard reads it", a.file, a.what)
			continue
		}
		claimed, convErr := strconv.Atoi(m[1])
		if convErr != nil {
			t.Fatalf("%s: %v", a.file, convErr)
		}
		if claimed != n {
			t.Errorf("%s says %d %s; the schema has %d", a.file, claimed, a.what, n)
		}
	}
}
