package main

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
)

// minGroup is how many strings in one declaration make it a row of its own.
const minGroup = 5

// Guards that read the reference sites today. The names are checked by
// TestEveryNamedTestExists, which reads this file.
const (
	guardRuns  = "TestCIRunPatternsNameExistingTests"
	guardPaths = "TestCIPathFiltersNameExistingFiles"
	guardNamed = "TestEveryNamedTestExists"
	guardKeys  = "TestOnlyListedTestsReadTheStyleSheetsAndScripts"
)

type invRow struct {
	where  string
	n      int
	caught string
}

// inventory prints where the repository names an existing test outside the
// test's own declaration and doc comment, and which guard notices a rename.
func inventory(s *snapshot, w io.Writer) error {
	p := &printer{w: w}
	d, err := scanDecls(s)
	if err != nil {
		return err
	}
	ms := mentions(s, d)
	tests, mains := 0, 0
	for _, x := range d.all {
		if x.Main {
			mains++
		} else if strings.HasPrefix(x.Name, "Test") {
			tests++
		}
	}
	dup := duplicated(d)
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	t := &printer{w: tw}
	t.printf("Test functions\t%d\n", tests)
	t.printf("TestMain functions\t%d\n", mains)
	t.printf("Names declared in several directories\t%d (%s)\n", len(dup), mostCopies(dup, d))
	t.println()
	t.println("where an existing test is named\tmentions\tcaught today by")
	rows, ownDoc := classify(ms)
	total := 0
	for _, r := range rows {
		t.printf("%s\t%d\t%s\n", r.where, r.n, r.caught)
		total += r.n
	}
	t.printf("total\t%d\t\n", total)
	t.printf("own doc comment lines (not counted above)\t%d\t\n", ownDoc)
	t.println()
	t.printf("test file paths\t%s\t\n", pathSummary(ms))
	if t.err != nil {
		return t.err
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	printStale(p, ms, d)
	return p.err
}

func duplicated(d *declared) map[string]int {
	out := map[string]int{}
	for name, dirs := range d.dirsByName() {
		if len(dirs) > 1 {
			out[name] = len(dirs)
		}
	}
	return out
}

func mostCopies(dup map[string]int, d *declared) string {
	best, bestN := "", 0
	for _, name := range sortedKeys(dup) {
		count := 0
		for _, x := range d.all {
			if x.Name == name {
				count++
			}
		}
		if count > bestN {
			best, bestN = name, count
		}
	}
	if best == "" {
		return "none"
	}
	return fmt.Sprintf("%s has %d copies", best, bestN)
}

// classify groups the mentions of existing tests into the rows of the table.
// A group of strings in one declaration (a table of test names) is a row of
// its own once it has minGroup strings.
func classify(ms []mention) (rows []invRow, ownDoc int) {
	groups := map[string]int{}
	for _, m := range ms {
		if m.What == whatTest && m.Exists && m.Cat == catTestString && !m.IsKey {
			groups[m.File+" "+m.Decl]++
		}
	}
	merged := map[string]*invRow{}
	var labels []string
	for _, m := range ms {
		if m.What != whatTest || !m.Exists {
			continue
		}
		if m.Cat == catOwnDoc {
			ownDoc++
			continue
		}
		label, caught := rowFor(m)
		if m.Cat == catTestString && !m.IsKey && groups[m.File+" "+m.Decl] < minGroup {
			label, caught = "other _test.go strings and comments", "nothing: "+guardNamed+" skips _test.go"
		}
		if merged[label] == nil {
			merged[label] = &invRow{where: label, caught: caught}
			labels = append(labels, label)
		}
		merged[label].n++
	}
	slices.Sort(labels)
	slices.SortStableFunc(labels, func(a, b string) int { return rank(a) - rank(b) })
	for _, l := range labels {
		rows = append(rows, *merged[l])
	}
	return rows, ownDoc
}

// rowFor names the table row of a mention and the guard that sees it.
func rowFor(m mention) (label, caught string) {
	switch {
	case m.Cat == catWorkflow:
		return "-run in workflows", guardRuns
	case m.Cat == catMakefile:
		return "-run in the Makefile", guardRuns + " and workflow-check"
	case m.IsKey:
		return "file#Test keys in " + m.File, "its own stale-row check (" + guardKeys + ")"
	case m.Cat == catTestString:
		return "strings in " + m.File + " " + m.Decl, "nothing: " + guardNamed + " skips _test.go"
	case m.Cat == catTestIdent:
		return "identifiers in _test.go files (calls, values)", "the compiler"
	case m.Cat == catGoComment && strings.HasSuffix(m.File, "_test.go"):
		return "other _test.go strings and comments", "nothing: " + guardNamed + " skips _test.go"
	case m.Cat == catMarkdown:
		return "markdown: " + m.File, guardNamed
	case m.Cat == catOther:
		return "other files: " + m.File, "nothing (not .md, .go or .sql)"
	}
	return "non-test Go comments and strings, .sql", guardNamed
}

// rank orders the table: CI first, then Go, then prose.
func rank(label string) int {
	switch {
	case strings.HasPrefix(label, "-run in workflows"):
		return 0
	case strings.HasPrefix(label, "-run in the Makefile"):
		return 1
	case strings.HasPrefix(label, "file#Test"):
		return 2
	case strings.HasPrefix(label, "strings in"):
		return 3
	case strings.HasPrefix(label, "other _test.go"), strings.HasPrefix(label, "identifiers"):
		return 4
	case strings.HasPrefix(label, "non-test"):
		return 5
	case strings.HasPrefix(label, "markdown"):
		return 6
	}
	return 7
}

// pathSummary counts the mentions of test file paths.
func pathSummary(ms []mention) string {
	n, distinct, existing := 0, map[string]bool{}, map[string]bool{}
	for _, m := range ms {
		if m.What != whatFile || m.Cat == catOwnDoc {
			continue
		}
		n++
		distinct[m.Name] = true
		if m.Exists {
			existing[m.Name] = true
		}
	}
	return fmt.Sprintf("%d mentions, %d distinct, %d existing", n, len(distinct), len(existing))
}

// printStale lists the names and paths a file mentions that do not exist. A
// -run pattern that names only the start of a test's name is a prefix, not a
// stale name.
func printStale(p *printer, ms []mention, d *declared) {
	declaredNames := make([]string, 0, len(d.all))
	for _, x := range d.all {
		declaredNames = append(declaredNames, x.Name)
	}
	var stale, prefixes []string
	for _, m := range ms {
		if m.Exists || m.Cat == catOwnDoc {
			continue
		}
		line := fmt.Sprintf("%s:%d\t%s", m.File, m.Line, m.Name)
		isRun := m.Cat == catMakefile || m.Cat == catWorkflow
		if isRun && m.What == whatTest && slices.ContainsFunc(declaredNames, func(n string) bool { return strings.HasPrefix(n, m.Name) }) {
			prefixes = append(prefixes, line)
			continue
		}
		stale = append(stale, line)
	}
	slices.Sort(stale)
	slices.Sort(prefixes)
	p.printf("\n-run prefix patterns (a rename must keep them selecting the same tests): %d\n", len(prefixes))
	for _, line := range prefixes {
		p.println("  " + line)
	}
	p.printf("mentions of a name or path that does not exist: %d\n", len(stale))
	for _, line := range stale {
		p.println("  " + line)
	}
}
