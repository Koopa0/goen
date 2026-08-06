package pages_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestEveryNamedTestExists refuses a claim of enforcement with nothing behind it.
//
// # Why it is derived rather than a list
//
// This repository has now found FIVE comments naming a guarantee that no code
// kept, and every one of them was found by hand, months apart, by somebody who
// happened to look:
//
//   - product_search_documents carried a note claiming "exactly one writer" and
//     had none — the whole table was dead.
//   - Store.Remove shipped with 2FA under a comment saying "another admin does
//     this" and NO CALLER, so an admin who lost their phone was locked out
//     permanently.
//   - The header's hard-coded NavItem list named TestTopNavPointsAtRealCategories
//     as what kept it from drifting from the catalogue. That test was never
//     written, and 耳機 duly came to point at a dead link on every page at once.
//   - internal/payment/store.go names TestTheAwardWindowMatchesTheProgramme. The
//     guarantee is real and the test is called something else — and the REAL
//     test's own doc comment is about this exact failure mode, which is how
//     close this gets to being self-aware without being caught.
//   - migrations/001 named TestReportingCannotReadCredentialsOrPII as what asks,
//     per named table, whether the read-only role can read a credential. It did
//     not exist; nor did any assertion about SELECT for any role. The list it
//     claimed to guard had drifted by eight tables, one of them the outbox,
//     whose payloads carry live plaintext password-reset links.
//
// Fixing them one at a time has failed four times. The failure is not any
// individual comment — it is that a test NAME in prose costs nothing to write
// and nothing checks it, so the claim and the code drift for free.
//
// # What this asks
//
// Every identifier that looks like a Go test name, mentioned anywhere outside a
// test file, must resolve to a `func` that exists. It reads CLAUDE.md, the
// migrations, docs/ and every non-test .go and .sql file — the places a claim
// gets written — and it does not care WHAT the test asserts. A name that
// resolves is a name somebody can go and read; that is the whole guarantee, and
// it is the one that was missing.
func TestEveryNamedTestExists(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	// A Go test name: Test followed by an upper-case letter. Bounded on the left
	// so `subTestFoo` does not match, and on the right so the whole identifier
	// is captured rather than a prefix of it.
	named := regexp.MustCompile(`\bTest[A-Z][A-Za-z0-9_]*`)

	declared := map[string]bool{}
	type mention struct{ name, where string }
	var mentions []mention

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Nothing generated, vendored or checked out by a tool: a name in
			// there is not this repository's claim to keep.
			switch d.Name() {
			case ".git", "node_modules", "vendor", "assets", "screens":
				return filepath.SkipDir
			}
			// docs/reviews is a HISTORICAL record: each file says what a round
			// of review said at the time, and several name tests under the role
			// names goen used then (TestGoenAppIsNotSuperuser is
			// TestStoreIsNotSuperuser now). Correcting those would falsify the
			// record of what was proposed, which is the one thing those files
			// are for. A live claim belongs in CLAUDE.md, a decision record or
			// the code, and all three are read.
			if filepath.Base(filepath.Dir(path)) == "docs" && d.Name() == "reviews" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".go" && ext != ".sql" && ext != ".md" {
			return nil
		}
		//nolint:gosec // G304: path comes from walking this repository's own
		// source tree, not from any request.
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(body)
		rel, _ := filepath.Rel(root, path)

		if strings.HasSuffix(path, "_test.go") {
			// Test files DECLARE. They are not read for mentions, for the reason
			// TestEveryViewModelFieldIsAssigned excludes them: a claim that only
			// a test makes to itself is not the claim this is about, and a
			// guard satisfied by its own error message is the failure mode that
			// test records.
			for _, line := range strings.Split(text, "\n") {
				if after, ok := strings.CutPrefix(line, "func Test"); ok {
					if i := strings.IndexAny(after, "("); i > 0 {
						declared["Test"+strings.TrimSpace(after[:i])] = true
					}
				}
			}
			return nil
		}
		for _, line := range strings.Split(text, "\n") {
			// The per-line escape, spelled like i18n-exempt because it is the
			// same idea: a line whose SUBJECT is a test that does not exist.
			// CLAUDE.md and internal/ui/layouts record TestTopNavPointsAtReal-
			// Categories precisely to say it was never written and what that
			// cost — dropping the name would delete the evidence, and writing
			// the test would be writing one for a design that no longer exists.
			if strings.Contains(line, "named-test-exempt:") {
				continue
			}
			for _, m := range named.FindAllString(line, -1) {
				mentions = append(mentions, mention{name: m, where: rel})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(declared) == 0 {
		t.Fatal("no test declarations were collected at all, so this guard is " +
			"asserting nothing — the scan for `func Test` has stopped matching")
	}
	if len(mentions) == 0 {
		t.Fatal("no test names were found mentioned outside test files, which " +
			"cannot be true of this repository — the pattern has stopped matching")
	}

	seen := map[string]bool{}
	var missing []string
	for _, m := range mentions {
		if declared[m.name] || seen[m.name+m.where] {
			continue
		}
		seen[m.name+m.where] = true
		missing = append(missing, m.name+" (named in "+m.where+")")
	}
	slices.Sort(missing)
	for _, m := range missing {
		t.Errorf("%s does not exist.\nA comment naming a test is a claim that "+
			"something is enforced. Write the test, correct the name, or delete "+
			"the sentence — this repository has shipped five of these, and each "+
			"one hid a real defect for months.", m)
	}
}
