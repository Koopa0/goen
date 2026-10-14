package main

import (
	"bytes"
	"go/format"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The fixture in testdata/repo is a small module with the situations the
// migration meets: a test name declared in two packages, a -run line that names
// one of them, a -run prefix, a file#Test key, a workflow paths: filter, prose
// that names a test, and a function that moves between files.

// fixture copies testdata/repo into a new directory and returns it.
func fixture(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	if err := os.CopyFS(dst, os.DirFS(filepath.Join("testdata", "repo"))); err != nil {
		t.Fatal(err)
	}
	return dst
}

// rowsOf parses map text; the header is added.
func rowsOf(t *testing.T, body string) []row {
	t.Helper()
	rows, err := parseMap("test", strings.NewReader(mapHeader+"\n"+body))
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func snap(t *testing.T, dir string) *snapshot {
	t.Helper()
	s, err := loadSnapshot(workTree{dir})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// applyIn applies rows to the tree in dir and writes the result.
func applyIn(t *testing.T, dir string, rows []row) *result {
	t.Helper()
	res, err := applyRows(snap(t, dir), rows)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeResult(dir, res); err != nil {
		t.Fatal(err)
	}
	return res
}

func text(t *testing.T, dir, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name))) //nolint:gosec // G304: a file of the fixture.
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func wantIn(t *testing.T, dir, name string, present, absent []string) {
	t.Helper()
	got := text(t, dir, name)
	for _, s := range present {
		if !strings.Contains(got, s) {
			t.Errorf("%s lacks %q:\n%s", name, s, got)
		}
	}
	for _, s := range absent {
		if strings.Contains(got, s) {
			t.Errorf("%s still has %q:\n%s", name, s, got)
		}
	}
}

const renameRows = "alpha/alpha_test.go\tTestParseEmpty\tTestParse\tParse\tprefix\trename\t\n" +
	"alpha/alpha_test.go\tTestSame\tTestAlphaSame\tStore_Decide\tcallee\trename\t\n"

// TestApplyRenamesEveryMention renames a unique name wherever it is written and
// a name declared in two packages only where it means the renamed one.
func TestApplyRenamesEveryMention(t *testing.T) {
	t.Parallel()
	dir := fixture(t)
	res := applyIn(t, dir, rowsOf(t, renameRows))

	wantIn(t, dir, "alpha/alpha_test.go",
		[]string{"// TestParse checks that", "func TestParse(", "// TestAlphaSame is alpha's own test", "func TestAlphaSame("},
		[]string{"TestParseEmpty", "func TestSame"})
	wantIn(t, dir, "beta/beta_test.go", []string{"// TestSame is beta's own test.", "func TestSame("}, []string{"TestAlphaSame"})
	wantIn(t, dir, "Makefile",
		[]string{"-run '^TestAlphaSame$$' ./alpha", "-run '^TestSame$$' ./beta"}, nil)
	wantIn(t, dir, ".github/workflows/ci.yml", []string{"-run '^TestParse$' ./alpha"}, []string{"TestParseEmpty"})
	wantIn(t, dir, "README.md", []string{"TestParse holds the empty-line rule", "TestSame is declared twice"}, []string{"TestParseEmpty"})
	wantIn(t, dir, "alpha/keys_test.go",
		[]string{`"alpha/alpha_test.go#TestParse":`, `"alpha/alpha_test.go#TestAlphaSame":`, `"beta/beta_test.go#TestSame":`},
		[]string{"TestParseEmpty"})

	if len(res.findings) != 1 || res.findings[0].File != "README.md" || !strings.Contains(res.findings[0].Msg, "TestSame is declared in alpha, beta") {
		t.Errorf("findings = %v, want exactly the README's mention of the name both packages declare", res.findings)
	}
	if res.pending != 2 || res.done != 0 {
		t.Errorf("applied %d rows and found %d done, want 2 and 0", res.pending, res.done)
	}
}

// TestApplyKeepsFormatting re-aligns what gofmt aligns: the keys of the map
// literal change length, and the file stays gofmt-clean.
func TestApplyKeepsFormatting(t *testing.T) {
	t.Parallel()
	dir := fixture(t)
	applyIn(t, dir, rowsOf(t, renameRows))
	for _, name := range []string{"alpha/keys_test.go", "alpha/alpha_test.go", "beta/beta_test.go"} {
		got := []byte(text(t, dir, name))
		want, err := format.Source(got)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is not gofmt-clean after apply:\n%s", name, got)
		}
	}
}

// TestApplyIsIdempotent applies a map twice. The second run changes nothing and
// says so; a branch that rebased onto a renamed tree relies on that.
func TestApplyIsIdempotent(t *testing.T) {
	t.Parallel()
	dir := fixture(t)
	rows := rowsOf(t, renameRows)
	applyIn(t, dir, rows)
	res := applyIn(t, dir, rows)
	if len(res.changed) != 0 || res.pending != 0 || res.done != 2 {
		t.Errorf("second apply changed %d files, applied %d rows, found %d done; want 0, 0, 2", len(res.changed), res.pending, res.done)
	}
}

// TestApplyRepairsAReferenceAddedAfterTheRename gives a tree whose declarations
// are renamed a new mention of an old name, as a rebased branch would, and
// applies the map again.
func TestApplyRepairsAReferenceAddedAfterTheRename(t *testing.T) {
	t.Parallel()
	dir := fixture(t)
	rows := rowsOf(t, renameRows)
	applyIn(t, dir, rows)
	notes := filepath.Join(dir, "NOTES.md")
	if err := os.WriteFile(notes, []byte("See TestParseEmpty.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	applyIn(t, dir, rows)
	wantIn(t, dir, "NOTES.md", []string{"See TestParse."}, []string{"TestParseEmpty"})
}

// TestApplyRefusesARowTheTreeContradicts names a test that does not exist.
func TestApplyRefusesARowTheTreeContradicts(t *testing.T) {
	t.Parallel()
	dir := fixture(t)
	rows := rowsOf(t, "alpha/alpha_test.go\tTestNoSuchTest\tTestOther\t-\thand\trename\t\n")
	if _, err := applyRows(snap(t, dir), rows); err == nil || !strings.Contains(err.Error(), "TestNoSuchTest is not declared") {
		t.Errorf("applyRows error = %v, want a refusal naming TestNoSuchTest", err)
	}
}

const moveRows = "alpha/alpha_test.go\tTestParseEmpty\tTestParseEmpty\tParse\tprefix\trename\talpha/parse_test.go\n"

// TestApplyMovesAFunctionToANewFile moves a test, with its doc comment, to a new
// file; the imports and the file#Test key follow it. The workflow's paths:
// filter names the old file beside the test, but the old file keeps other
// tests, so apply leaves the filter and says so.
func TestApplyMovesAFunctionToANewFile(t *testing.T) {
	t.Parallel()
	dir := fixture(t)
	res := applyIn(t, dir, rowsOf(t, moveRows))

	wantIn(t, dir, "alpha/parse_test.go",
		[]string{"package alpha_test", "// TestParseEmpty checks that an empty line has no fields.", "func TestParseEmpty(", `"strings"`, `"example.com/fix/alpha"`},
		nil)
	wantIn(t, dir, "alpha/alpha_test.go",
		[]string{"func TestSame(", "func TestAlphaKeeps("},
		[]string{"TestParseEmpty", `"strings"`})
	wantIn(t, dir, "alpha/keys_test.go", []string{`"alpha/parse_test.go#TestParseEmpty"`, `"alpha/alpha_test.go#TestSame"`}, []string{`"alpha/alpha_test.go#TestParseEmpty"`})
	wantIn(t, dir, ".github/workflows/ci.yml", []string{"- alpha/alpha_test.go", "-run '^TestParseEmpty$' ./alpha"}, []string{"parse_test.go"})
	for _, name := range []string{"alpha/parse_test.go", "alpha/alpha_test.go"} {
		got := []byte(text(t, dir, name))
		if want, err := format.Source(got); err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s is not gofmt-clean after the move (error %v):\n%s", name, err, got)
		}
	}
	if len(res.findings) != 1 || res.findings[0].File != ".github/workflows/ci.yml" || !strings.Contains(res.findings[0].Msg, "it keeps other tests") {
		t.Errorf("findings = %v, want only the workflow's filter", res.findings)
	}
}

// TestApplyFollowsAFileThatEmpties moves every test out of a file. The file
// goes, and the workflow filter that named it names the new file.
func TestApplyFollowsAFileThatEmpties(t *testing.T) {
	t.Parallel()
	dir := fixture(t)
	res := applyIn(t, dir, rowsOf(t, "alpha/alpha_test.go\tTestParseEmpty\tTestParseEmpty\t-\thand\trename\talpha/all_test.go\n"+
		"alpha/alpha_test.go\tTestSame\tTestSame\t-\thand\trename\talpha/all_test.go\n"+
		"alpha/alpha_test.go\tTestAlphaKeeps\tTestAlphaKeeps\t-\thand\trename\talpha/all_test.go\n"))
	if _, err := os.Stat(filepath.Join(dir, "alpha", "alpha_test.go")); err == nil {
		t.Error("alpha/alpha_test.go still exists although every test left it")
	}
	if !slices.Equal(res.removed, []string{"alpha/alpha_test.go"}) {
		t.Errorf("removed = %v, want alpha/alpha_test.go", res.removed)
	}
	wantIn(t, dir, "alpha/all_test.go", []string{"func TestParseEmpty(", "func TestSame(", "func TestAlphaKeeps("}, nil)
	wantIn(t, dir, ".github/workflows/ci.yml", []string{"- alpha/all_test.go"}, []string{"- alpha/alpha_test.go"})
	wantIn(t, dir, "alpha/keys_test.go", []string{`"alpha/all_test.go#TestSame"`, `"beta/beta_test.go#TestSame"`}, []string{`"alpha/alpha_test.go#`})
}

// TestApplyMovesAFunctionIntoAnExistingFile adds the imports the destination
// lacks.
func TestApplyMovesAFunctionIntoAnExistingFile(t *testing.T) {
	t.Parallel()
	dir := fixture(t)
	applyIn(t, dir, rowsOf(t, "alpha/alpha_test.go\tTestAlphaKeeps\tTestAlphaKeeps\t-\thand\trename\talpha/keys_test.go\n"))
	wantIn(t, dir, "alpha/keys_test.go",
		[]string{"func TestAlphaKeeps(", "func TestKeys(", `"example.com/fix/alpha"`, `"testing"`}, nil)
	wantIn(t, dir, "alpha/alpha_test.go", []string{"func TestSame("}, []string{"TestAlphaKeeps"})
}

// TestApplyRefusesAMoveBetweenBuildConfigurations will not put a function in a
// file with other build tags.
func TestApplyRefusesAMoveBetweenBuildConfigurations(t *testing.T) {
	t.Parallel()
	dir := fixture(t)
	tagged := "//go:build integration\n\npackage alpha_test\n\nimport \"testing\"\n\nfunc TestTagged(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(dir, "alpha", "tagged_integration_test.go"), []byte(tagged), 0o600); err != nil {
		t.Fatal(err)
	}
	rows := rowsOf(t, "alpha/alpha_test.go\tTestSame\tTestSame\t-\thand\trename\talpha/tagged_integration_test.go\n")
	if _, err := applyRows(snap(t, dir), rows); err == nil || !strings.Contains(err.Error(), "may not change the package or the build tags") {
		t.Errorf("applyRows error = %v, want a refusal to change the build tags", err)
	}
}

// TestApplyMovesAFileAndItsPath renames a file and every full path that names it.
func TestApplyMovesAFileAndItsPath(t *testing.T) {
	t.Parallel()
	dir := fixture(t)
	applyIn(t, dir, rowsOf(t, "alpha/keys_test.go\t-\t-\t-\thand\tmv-file\talpha/listing_test.go\n"))
	if _, err := os.Stat(filepath.Join(dir, "alpha", "keys_test.go")); err == nil {
		t.Error("alpha/keys_test.go still exists")
	}
	wantIn(t, dir, "alpha/listing_test.go", []string{"func TestKeys("}, nil)
}

// TestApplyThenInverseRestoresTheTree applies a map that renames, moves and
// renames a file, then its inverse, and compares every file with the fixture.
func TestApplyThenInverseRestoresTheTree(t *testing.T) {
	t.Parallel()
	dir := fixture(t)
	rows := rowsOf(t, renameRows+moveRowsAfterRename+"alpha/keys_test.go\t-\t-\t-\thand\tmv-file\talpha/listing_test.go\n")
	applyIn(t, dir, rows)
	applyIn(t, dir, inverse(rows))
	if diff := diffTrees(t, fixture(t), dir); diff != "" {
		t.Errorf("the inverse did not restore the tree:\n%s", diff)
	}
}

const moveRowsAfterRename = "alpha/alpha_test.go\tTestAlphaKeeps\tTestAlphaKeeps\t-\thand\trename\talpha/more_test.go\n"

func diffTrees(t *testing.T, want, got string) string {
	t.Helper()
	list := func(root string) []string {
		var out []string
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				rel, _ := filepath.Rel(root, p)
				out = append(out, filepath.ToSlash(rel))
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(out)
		return out
	}
	var diff []string
	a, b := list(want), list(got)
	if !slices.Equal(a, b) {
		diff = append(diff, "files differ: want "+strings.Join(a, ",")+" got "+strings.Join(b, ","))
	}
	for _, name := range a {
		if slices.Contains(b, name) && text(t, want, name) != text(t, got, name) {
			diff = append(diff, name+" differs")
		}
	}
	return strings.Join(diff, "\n")
}

// applied returns a copy of the fixture, and the same copy with rows applied.
func applied(t *testing.T, rows []row) (before, after string) {
	t.Helper()
	before, after = fixture(t), fixture(t)
	applyIn(t, after, rows)
	return before, after
}

func verifyDirs(t *testing.T, before, after string, rows []row) *verification {
	t.Helper()
	v, err := verify(snap(t, before), snap(t, after), rows)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func wantFindings(t *testing.T, v *verification, parts ...string) {
	t.Helper()
	all := make([]string, 0, len(v.findings))
	for _, f := range v.findings {
		all = append(all, f.String())
	}
	joined := strings.Join(all, "\n")
	if len(parts) == 0 && len(all) > 0 {
		t.Errorf("verify reported problems:\n%s", joined)
	}
	for _, p := range parts {
		if !strings.Contains(joined, p) {
			t.Errorf("verify did not report %q; it reported:\n%s", p, joined)
		}
	}
	if len(parts) > 0 && len(all) == 0 {
		t.Error("verify reported nothing")
	}
}

// TestVerifyAcceptsWhatApplyDid is the green case: the tree after apply passes,
// apart from the prefix pattern that now selects a second test, which the next
// test holds on its own.
func TestVerifyAcceptsWhatApplyDid(t *testing.T) {
	t.Parallel()
	rows := rowsOf(t, "alpha/alpha_test.go\tTestParseEmpty\tTestParse\tParse\tprefix\trename\talpha/parse_test.go\n")
	before, after := applied(t, rows)
	v := verifyDirs(t, before, after, rows)
	wantFindings(t, v)
	if v.before["no tags"] != 5 || v.now["no tags"] != 5 {
		t.Errorf("tests before and after = %d and %d, want 5 and 5", v.before["no tags"], v.now["no tags"])
	}
}

// TestVerifyHoldsAPrefixToWhatItSelected renames a test so that the -run prefix
// ^TestAlpha selects a second test.
func TestVerifyHoldsAPrefixToWhatItSelected(t *testing.T) {
	t.Parallel()
	rows := rowsOf(t, renameRows)
	before, after := applied(t, rows)
	wantFindings(t, verifyDirs(t, before, after, rows),
		"Makefile:8: -run \"^TestAlpha\" selected [alpha TestAlphaKeeps] at the base and now selects [alpha TestAlphaKeeps, alpha TestAlphaSame]")
}

// TestVerifyNamesAnOldNameThatSurvives edits a doc comment back.
func TestVerifyNamesAnOldNameThatSurvives(t *testing.T) {
	t.Parallel()
	rows := rowsOf(t, "alpha/alpha_test.go\tTestParseEmpty\tTestParse\tParse\tprefix\trename\t\n")
	before, after := applied(t, rows)
	path := filepath.Join(after, "alpha", "alpha_test.go")
	body := strings.Replace(text(t, after, "alpha/alpha_test.go"), "// TestParse checks", "// TestParseEmpty checks", 1)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	wantFindings(t, verifyDirs(t, before, after, rows), "alpha/alpha_test.go:10: the old name TestParseEmpty survives")
}

// TestVerifyNamesAChangedBody edits one renamed test's body.
func TestVerifyNamesAChangedBody(t *testing.T) {
	t.Parallel()
	rows := rowsOf(t, "alpha/alpha_test.go\tTestParseEmpty\tTestParse\tParse\tprefix\trename\t\n")
	before, after := applied(t, rows)
	body := strings.Replace(text(t, after, "alpha/alpha_test.go"), "len(got) != 0", "len(got) > 1", 1)
	if err := os.WriteFile(filepath.Join(after, "alpha", "alpha_test.go"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	v := verifyDirs(t, before, after, rows)
	wantFindings(t, v, "body of TestParse changed: hash ")
}

// TestVerifyRefusesALostTest deletes a test that no row mentions.
func TestVerifyRefusesALostTest(t *testing.T) {
	t.Parallel()
	rows := rowsOf(t, "alpha/alpha_test.go\tTestParseEmpty\tTestParse\tParse\tprefix\trename\t\n")
	before, after := applied(t, rows)
	body, _, _ := strings.Cut(text(t, after, "beta/beta_test.go"), "// TestSame")
	if err := os.WriteFile(filepath.Join(after, "beta", "beta_test.go"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	wantFindings(t, verifyDirs(t, before, after, rows), "TestSame was declared in beta and is gone: no test may be lost")
}

// TestVerifyRefusesANewTest adds a test that no row accounts for.
func TestVerifyRefusesANewTest(t *testing.T) {
	t.Parallel()
	rows := rowsOf(t, "alpha/alpha_test.go\tTestParseEmpty\tTestParse\tParse\tprefix\trename\t\n")
	before, after := applied(t, rows)
	extra := "package beta\n\nimport \"testing\"\n\nfunc TestExtra(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(after, "beta", "extra_test.go"), []byte(extra), 0o600); err != nil {
		t.Fatal(err)
	}
	wantFindings(t, verifyDirs(t, before, after, rows), "TestExtra is a test that neither the base nor the map accounts for")
}

// TestVerifyNamesAPathThatVanished moves a test file by hand and leaves the
// paths that name it.
func TestVerifyNamesAPathThatVanished(t *testing.T) {
	t.Parallel()
	rows := rowsOf(t, "alpha/alpha_test.go\tTestParseEmpty\tTestParse\tParse\tprefix\trename\t\n")
	before, after := applied(t, rows)
	if err := os.Rename(filepath.Join(after, "alpha", "alpha_test.go"), filepath.Join(after, "alpha", "main_test.go")); err != nil {
		t.Fatal(err)
	}
	wantFindings(t, verifyDirs(t, before, after, rows), "names alpha/alpha_test.go, which no longer exists")
}

// TestVerifyRefusesAnUnfinishedRow holds a map to its todo rows.
func TestVerifyRefusesAnUnfinishedRow(t *testing.T) {
	t.Parallel()
	rows := rowsOf(t, "alpha/alpha_test.go\tTestSame\tTestSame\t\thand\ttodo\t\n")
	dir := fixture(t)
	wantFindings(t, verifyDirs(t, dir, dir, rows), "TestSame has an unfinished row")
}

// TestVerifyAcceptsHandWorkRows lets a row record a deletion and a fold into
// another test, and holds each to its result: the tests are gone and nothing
// else is lost or added. The mentions of the two names, and the -run line that
// selected one of them, are still there, and verify says so.
func TestVerifyAcceptsHandWorkRows(t *testing.T) {
	t.Parallel()
	rows := rowsOf(t, "alpha/alpha_test.go\tTestSame\t-\t-\thand\taudit-delete #1\t\n"+
		"alpha/alpha_test.go\tTestParseEmpty\tempty\t-\thand\tcase-of:TestAlphaKeeps\t\n")
	before, after := fixture(t), fixture(t)
	head, rest, _ := strings.Cut(text(t, after, "alpha/alpha_test.go"), "// TestParseEmpty")
	_, keeps, _ := strings.Cut(rest, "// TestAlphaKeeps")
	body := head + "// TestAlphaKeeps" + keeps
	if err := os.WriteFile(filepath.Join(after, "alpha", "alpha_test.go"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	v := verifyDirs(t, before, after, rows)
	for _, f := range v.findings {
		if !strings.Contains(f.Msg, "the old name") && !strings.Contains(f.Msg, "-run") {
			t.Errorf("unexpected problem: %v", f)
		}
	}
	if len(v.findings) == 0 {
		t.Error("verify did not report the mentions of the two deleted names")
	}
}

// TestParseMapRefusesBadRows checks the map reader.
func TestParseMapRefusesBadRows(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, body, want string
	}{
		{"header", "file\told\n", "header is"},
		{"columns", mapHeader + "\nalpha/a_test.go\tTestA\n", "2 columns, want 7"},
		{"action", mapHeader + "\nalpha/a_test.go\tTestA\tTestB\t\t\tmove\t\n", `unknown action "move"`},
		{"old", mapHeader + "\nalpha/a_test.go\ttestA\tTestB\t\t\trename\t\n", "is not a Test name"},
		{"file", mapHeader + "\nalpha/a.go\tTestA\tTestB\t\t\trename\t\n", "not a slash path to a _test.go file"},
		{"newfile", mapHeader + "\nalpha/a_test.go\tTestA\tTestB\t\t\trename\tbeta/b_test.go\n", "must be a _test.go file in alpha"},
		{"twice", mapHeader + "\nalpha/a_test.go\tTestA\tTestB\t\t\trename\t\nalpha/a_test.go\tTestA\tTestC\t\t\trename\t\n", "has two rows"},
		{"same name", mapHeader + "\nalpha/a_test.go\tTestA\tTestC\t\t\trename\t\nalpha/b_test.go\tTestB\tTestC\t\t\trename\t\n", "two tests become TestC"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := parseMap("m", strings.NewReader(tt.body)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("parseMap error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

// TestInventoryCountsTheFixture reads the fixture's reference sites.
func TestInventoryCountsTheFixture(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := inventory(snap(t, fixture(t)), &out); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for line := range strings.SplitSeq(out.String(), "\n") {
		lines = append(lines, strings.Join(strings.Fields(line), " "))
	}
	for _, want := range []string{
		"Test functions 5",
		"Names declared in several directories 1 (TestSame has 2 copies)",
		"-run in the Makefile 2 TestCIRunPatternsNameExistingTests and workflow-check",
		"-run in workflows 1 TestCIRunPatternsNameExistingTests",
		"file#Test keys in alpha/keys_test.go 3 its own stale-row check (TestOnlyListedTestsReadTheStyleSheetsAndScripts)",
		"markdown: README.md 2 TestEveryNamedTestExists",
		"Makefile:8 TestAlpha",
	} {
		if !slices.Contains(lines, want) {
			t.Errorf("inventory output lacks the line %q:\n%s", want, out.String())
		}
	}
}

// TestProposeFindsTheUnit proposes names for the fixture by prefix and by the
// unit the body calls.
func TestProposeFindsTheUnit(t *testing.T) {
	dir := fixture(t)
	rows, err := propose(dir, snap(t, dir), "alpha")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.Old] = r.New + " " + r.Unit + " " + r.Source + " " + r.Action
	}
	want := map[string]string{
		"TestParseEmpty": "TestParseEmpty Parse prefix rename",
		"TestSame":       "TestStore_DecideSame Store_Decide callee rename",
		"TestAlphaKeeps": "TestParseAlphaKeeps Parse callee rename",
		"TestKeys":       "TestKeys  hand todo",
	}
	for old, w := range want {
		if got[old] != w {
			t.Errorf("proposal for %s = %q, want %q", old, got[old], w)
		}
	}
}

// TestNames checks the pieces propose builds names from.
func TestNames(t *testing.T) {
	t.Parallel()
	units := map[string]string{"Store": "Store", "StoreDecide": "Store_Decide", "Parse": "Parse", "Pars": "Pars"}
	for _, tt := range []struct {
		rest, label, tail string
		ok                bool
	}{
		{"StoreDecideSplitReturn", "Store_Decide", "SplitReturn", true},
		{"StoreHolds", "Store", "Holds", true},
		{"ParseRejectsEmpty", "Parse", "RejectsEmpty", true},
		{"Parsed", "", "", false},
		{"Unrelated", "", "", false},
	} {
		label, tail, ok := byPrefix(tt.rest, units)
		if label != tt.label || tail != tt.tail || ok != tt.ok {
			t.Errorf("byPrefix(%q) = %q, %q, %v; want %q, %q, %v", tt.rest, label, tail, ok, tt.label, tt.tail, tt.ok)
		}
	}
	if got, want := scenario("IsTheShortFormOfADate"), "ShortFormDate"; got != want {
		t.Errorf("scenario = %q, want %q", got, want)
	}
	if got, want := fit("TestStore_Decide", "SplitReturnResumesAfterCredit"), "TestStore_DecideSplitReturnResumesAfter"; got != want && len(got) > len("Test")+maxName {
		t.Errorf("fit = %q, want at most %d characters after Test", got, maxName)
	}
}

// gitIn runs git in dir.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := gitOutput(dir, append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...); err != nil {
		t.Fatal(err)
	}
}

// TestRunRoundTripInAGitCheckout drives the command line the way a worker does:
// commit the fixture, write a map, apply it, verify against HEAD, apply the
// inverse, and see git report no difference.
func TestRunRoundTripInAGitCheckout(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := fixture(t)
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "fixture")
	mapFile := filepath.Join(t.TempDir(), "alpha.tsv")
	if err := os.WriteFile(mapFile, []byte(mapHeader+"\n"+renameRows+moveRowsAfterRename), 0o600); err != nil {
		t.Fatal(err)
	}

	steps := []struct {
		args    []string
		want    int
		out     string
		errText string
	}{
		// beta also declares TestSame, so the README's mention cannot be placed:
		// apply writes the rest and exits 1.
		{[]string{"apply", mapFile}, 1, "apply: 3 rows applied", "README.md:3: TestSame is declared in alpha, beta"},
		// Renaming TestSame makes the Makefile's ^TestAlpha prefix select a second test.
		{[]string{"verify", mapFile}, 1, "-tags integration: 5 tests before, 5 after", `-run "^TestAlpha" selected`},
	}
	for _, step := range steps {
		var stdout, stderr bytes.Buffer
		if got := run(append([]string{"-C", dir}, step.args...), &stdout, &stderr); got != step.want {
			t.Errorf("testrename %v exited %d, want %d\nstdout: %s\nstderr: %s", step.args, got, step.want, &stdout, &stderr)
		}
		if !strings.Contains(stdout.String(), step.out) || !strings.Contains(stderr.String(), step.errText) {
			t.Errorf("testrename %v printed %q and %q, want %q and %q", step.args, stdout.String(), stderr.String(), step.out, step.errText)
		}
	}
	var stdout, stderr bytes.Buffer
	if got := run([]string{"-C", dir, "apply", "-inverse", mapFile}, &stdout, &stderr); got != 0 {
		t.Fatalf("apply -inverse exited %d: %s %s", got, &stdout, &stderr)
	}
	if status := gitOutputIn(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("the inverse left changes behind:\n%s", status)
	}
}

func gitOutputIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitOutput(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
