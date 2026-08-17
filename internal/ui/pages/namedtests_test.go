package pages_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestEveryNamedTestExists refuses a claim of enforcement with nothing behind it:
// every Go test name mentioned outside a test file — in CLAUDE.md, a migration,
// docs/ or any .go or .sql file — must resolve to a func that exists.
func TestEveryNamedTestExists(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	named := regexp.MustCompile(`\bTest[A-Z][A-Za-z0-9_]*`)

	declared := map[string]bool{}
	type mention struct{ name, where string }
	var mentions []mention

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "assets", "screens":
				return filepath.SkipDir
			}
			// docs/reviews records what each review round said at the time, under
			// names since changed; correcting those would falsify the record.
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
			// Test files declare; they are not read for mentions, or this guard
			// could be satisfied by its own error message.
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
			// The per-line escape, for a line whose subject is a test that was
			// never written and whose absence is the thing being recorded.
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
