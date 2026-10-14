package repocheck_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// A `go test -run PATTERN ./pkg` that matches nothing exits 0 and prints
// "[no tests to run]". When a test moves to another package, a CI line that
// names it by pattern keeps passing without running it, and a workflow whose
// paths: filter names a file that moved stops firing on the change that
// matters. These two tests make both failures loud.

// goTestRun is one `go test` command that selects tests with -run.
type goTestRun struct {
	file    string // slash path relative to the repository root
	line    int
	pattern string
	pkgs    []string // package arguments as written, such as ./internal/ui/pages
}

// TestCIRunPatternsNameExistingTests requires every `go test -run PATTERN
// ./pkg` in the Makefile and in the workflows' run: steps to match at least
// one test function declared in that package.
func TestCIRunPatternsNameExistingTests(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

	runs := makefileGoTestRuns(t, root)
	makefileCount := len(runs)
	runs = append(runs, workflowGoTestRuns(t, root)...)

	const floor = 10
	if len(runs) < floor {
		t.Fatalf("found %d `go test -run` commands (%d in the Makefile), want at least %d: the extraction no longer reads the CI lines it guards",
			len(runs), makefileCount, floor)
	}

	declared := map[string][]string{} // package directory -> Test function names
	for _, run := range runs {
		re, err := regexp.Compile(topLevelRunPattern(run.pattern))
		if err != nil {
			t.Errorf("%s:%d: -run pattern %q does not compile: %v", run.file, run.line, run.pattern, err)
			continue
		}
		matched := slices.ContainsFunc(run.pkgs, func(pkg string) bool {
			return slices.ContainsFunc(declaredTests(t, root, pkg, declared), re.MatchString)
		})
		if !matched {
			t.Errorf("%s:%d: -run pattern %q matches no test function declared in %s (go test would print [no tests to run] and exit 0)",
				run.file, run.line, run.pattern, strings.Join(run.pkgs, " "))
		}
	}
}

// topLevelRunPattern returns the part of a -run pattern that selects top-level
// tests: go test splits the pattern at slashes and applies the first element
// to the test function's name, the rest to its subtests.
func topLevelRunPattern(pattern string) string {
	first, _, _ := strings.Cut(pattern, "/")
	return first
}

// declaredTests lists the Test functions declared in the *_test.go files of
// one package argument, whatever their build tags. A pattern ending in /...
// covers the package and everything below it. cache holds the answer per
// directory.
func declaredTests(t *testing.T, root, pkg string, cache map[string][]string) []string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(pkg, "/...")))
	if !strings.HasSuffix(pkg, "/...") {
		return testsInDir(t, dir, cache)
	}
	var tests []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			tests = append(tests, testsInDir(t, p, cache)...)
		}
		return nil
	})
	if err != nil {
		t.Errorf("package %s: %v", pkg, err)
	}
	return tests
}

func testsInDir(t *testing.T, dir string, cache map[string][]string) []string {
	t.Helper()
	if tests, ok := cache[dir]; ok {
		return tests
	}
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []string{}
	fset := token.NewFileSet()
	for _, file := range files {
		parsed, parseErr := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Errorf("%s: %v", file, parseErr)
			continue
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
				tests = append(tests, fn.Name.Name)
			}
		}
	}
	cache[dir] = tests
	return tests
}

// makefileGoTestRuns reads the Makefile line by line. The Makefile writes $$
// for a shell $; comment lines, including recipe lines that start @#, are
// prose and are skipped.
func makefileGoTestRuns(t *testing.T, root string) []goTestRun {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, "Makefile")) //nolint:gosec // G304: a fixed file at the repository root.
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.ReplaceAll(string(body), "$$", "$"), "\n")
	return goTestRunsIn("Makefile", lines, 1)
}

// workflowGoTestRuns reads every run: scalar of every workflow. It does not
// go through readCIWorkflow, which refuses a workflow that mentions secrets.
func workflowGoTestRuns(t *testing.T, root string) []goTestRun {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var runs []goTestRun
	for _, file := range files {
		body, readErr := os.ReadFile(file) //nolint:gosec // G304: globbed from the repository's own workflow directory.
		if readErr != nil {
			t.Fatal(readErr)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(body, &doc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		rel := ".github/workflows/" + filepath.Base(file)
		walkYAML(&doc, func(key, value *yaml.Node) {
			if key.Value != "run" || value.Kind != yaml.ScalarNode {
				return
			}
			// A block scalar's text starts on the line after its | or >.
			first := value.Line
			if value.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
				first++
			}
			runs = append(runs, goTestRunsIn(rel, strings.Split(value.Value, "\n"), first)...)
		})
	}
	return runs
}

func walkYAML(n *yaml.Node, visit func(key, value *yaml.Node)) {
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			visit(n.Content[i], n.Content[i+1])
		}
	}
	for _, child := range n.Content {
		walkYAML(child, visit)
	}
}

// goTestRunsIn extracts the `go test` commands that carry both -run and a
// package path. Lines ending in a backslash continue on the next line.
func goTestRunsIn(file string, lines []string, firstLine int) []goTestRun {
	var runs []goTestRun
	for i := 0; i < len(lines); i++ {
		start := i
		logical := lines[i]
		for strings.HasSuffix(logical, `\`) && i+1 < len(lines) {
			i++
			logical = strings.TrimSuffix(logical, `\`) + " " + lines[i]
		}
		trimmed := strings.TrimLeft(logical, " \t@-+")
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		for _, words := range shellCommands(logical) {
			if run, ok := parseGoTest(words); ok {
				run.file, run.line = file, firstLine+start
				runs = append(runs, run)
			}
		}
	}
	return runs
}

// parseGoTest reads one command's words. Both argument orders occur: -run
// before the package and after it.
func parseGoTest(words []string) (goTestRun, bool) {
	var run goTestRun
	at := -1
	for i := 0; i+1 < len(words); i++ {
		if words[i] == "go" && words[i+1] == "test" {
			at = i + 2
			break
		}
	}
	if at < 0 {
		return run, false
	}
	hasRun := false
	for i := at; i < len(words); i++ {
		w := words[i]
		switch {
		case w == "-run" && i+1 < len(words):
			run.pattern, hasRun = words[i+1], true
			i++
		case strings.HasPrefix(w, "-run="):
			run.pattern, hasRun = strings.TrimPrefix(w, "-run="), true
		case strings.HasPrefix(w, "./"):
			run.pkgs = append(run.pkgs, w)
		}
	}
	return run, hasRun && len(run.pkgs) > 0
}

// shellCommands splits one logical line into commands at unquoted ; & | and
// into words at unquoted blanks, removing single and double quotes. It is not
// a shell: it understands exactly what the CI lines use.
func shellCommands(line string) [][]string {
	var (
		commands [][]string
		words    []string
		word     strings.Builder
		inWord   bool
		quote    rune
	)
	endWord := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	endCommand := func() {
		endWord()
		if len(words) > 0 {
			commands = append(commands, words)
			words = nil
		}
	}
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ';' || r == '&' || r == '|':
			endCommand()
		case r == ' ' || r == '\t':
			endWord()
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	endCommand()
	return commands
}

// pathFilterWorkflow is the part of a workflow the path-filter guard reads.
// An event is a mapping that may carry paths and paths-ignore lists, but
// schedule is a list and an event may be empty, so each is decoded on its own.
type pathFilterWorkflow struct {
	On map[string]yaml.Node `yaml:"on"`
}

type pathFilters struct {
	Paths       []yaml.Node `yaml:"paths"`
	PathsIgnore []yaml.Node `yaml:"paths-ignore"`
}

// TestCIPathFiltersNameExistingFiles requires every paths: and paths-ignore:
// entry of every workflow to name something that exists. A filter that names a
// file which has since moved never fires, so the workflow silently stops
// running on the change it exists for.
func TestCIPathFiltersNameExistingFiles(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	files, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}

	var repoFiles []string // slash paths relative to root, loaded on the first glob entry
	entries := 0
	for _, file := range files {
		body, readErr := os.ReadFile(file) //nolint:gosec // G304: globbed from the repository's own workflow directory.
		if readErr != nil {
			t.Fatal(readErr)
		}
		var workflow pathFilterWorkflow
		if err := yaml.Unmarshal(body, &workflow); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		rel := ".github/workflows/" + filepath.Base(file)
		for event, eventNode := range workflow.On {
			var filters pathFilters
			if eventNode.Kind == yaml.MappingNode {
				if err := eventNode.Decode(&filters); err != nil {
					t.Fatalf("%s: on.%s: %v", file, event, err)
				}
			}
			for _, node := range slices.Concat(filters.Paths, filters.PathsIgnore) {
				entries++
				entry := strings.TrimPrefix(node.Value, "!")
				if !strings.ContainsAny(entry, "*?[") {
					if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(entry))); statErr != nil {
						t.Errorf("%s:%d: on.%s path filter %q names no file or directory in the repository", rel, node.Line, event, node.Value)
					}
					continue
				}
				if repoFiles == nil {
					repoFiles = listRepoFiles(t, root)
				}
				re, reErr := regexp.Compile(globRegexp(entry))
				if reErr != nil {
					t.Errorf("%s:%d: on.%s path filter %q is not a valid glob: %v", rel, node.Line, event, node.Value, reErr)
					continue
				}
				if !slices.ContainsFunc(repoFiles, re.MatchString) {
					t.Errorf("%s:%d: on.%s path filter %q matches no file in the repository", rel, node.Line, event, node.Value)
				}
			}
		}
	}

	const floor = 20
	if entries < floor {
		t.Fatalf("found %d path filter entries, want at least %d: the extraction no longer reads the filters it guards", entries, floor)
	}
}

// listRepoFiles returns every file in the working tree as a slash path
// relative to root, leaving out version control, dependency and scratch
// directories (a hidden directory other than .github).
func listRepoFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if p != root && (name == "node_modules" || (strings.HasPrefix(name, ".") && name != ".github")) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// globRegexp translates a GitHub Actions path filter into an anchored regular
// expression: ** crosses directory separators, * and ? do not, and [...] is a
// character class.
func globRegexp(glob string) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		switch c := glob[i]; c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				i++
				if i+1 < len(glob) && glob[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '[':
			end := strings.IndexByte(glob[i:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := strings.Replace(glob[i+1:i+end], "!", "^", 1)
			fmt.Fprintf(&b, "[%s]", class)
			i += end
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return b.String()
}
