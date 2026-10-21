package main

import (
	"path"
	"regexp"
	"strings"
)

// goTestRun is one `go test` command that selects tests with -run, as
// internal/repocheck's TestCIRunPatternsNameExistingTests reads it.
type goTestRun struct {
	file        string
	line        int // first line of the command
	last        int // last line of the command
	pattern     string
	pkgs        []string // package arguments as written: ./internal/ui/layouts
	integration bool     // -tags integration
}

// isCommandFile reports whether name holds shell command lines that run tests.
func isCommandFile(name string) bool {
	return name == "Makefile" || (strings.HasPrefix(name, ".github/workflows/") && strings.HasSuffix(name, ".yml")) ||
		strings.HasSuffix(name, ".sh")
}

// runsIn extracts the go test commands that carry -run and a package path from
// the Makefile, the workflows and the shell scripts. Lines ending in a
// backslash continue on the next line; comment lines are prose. The Makefile
// writes $$ for a shell $.
func runsIn(s *snapshot) []goTestRun {
	var runs []goTestRun
	for _, name := range s.paths {
		if !isCommandFile(name) {
			continue
		}
		body := string(s.data[name])
		if name == "Makefile" {
			body = strings.ReplaceAll(body, "$$", "$")
		}
		runs = append(runs, goTestRunsIn(name, strings.Split(body, "\n"))...)
	}
	return runs
}

func goTestRunsIn(file string, lines []string) []goTestRun {
	var runs []goTestRun
	for i := 0; i < len(lines); i++ {
		start := i
		logical := lines[i]
		for strings.HasSuffix(logical, `\`) && i+1 < len(lines) {
			i++
			logical = strings.TrimSuffix(logical, `\`) + " " + lines[i]
		}
		if strings.HasPrefix(strings.TrimLeft(logical, " \t@-+"), "#") {
			continue
		}
		for _, words := range shellCommands(logical) {
			if run, ok := parseGoTest(words); ok {
				run.file, run.line, run.last = file, start+1, i+1
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
	at := goTestArgs(words)
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
		case w == "-tags" && i+1 < len(words):
			run.integration = run.integration || strings.Contains(words[i+1], "integration")
			i++
		case strings.HasPrefix(w, "-tags="):
			run.integration = run.integration || strings.Contains(w, "integration")
		case strings.HasPrefix(w, "./"):
			run.pkgs = append(run.pkgs, w)
		}
	}
	return run, hasRun && len(run.pkgs) > 0
}

// goTestArgs returns the index of the word after "go test", or -1.
func goTestArgs(words []string) int {
	for i := 0; i+1 < len(words); i++ {
		if words[i] == "go" && words[i+1] == "test" {
			return i + 2
		}
	}
	return -1
}

// shellCommands splits one logical line into commands at unquoted ; & | and
// into words at unquoted blanks, removing single and double quotes. It is not
// a shell: it understands what the CI lines use.
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

// topLevelPattern is the part of a -run pattern that selects top-level tests:
// go test splits the pattern at slashes and applies the first element to the
// test function's name.
func topLevelPattern(pattern string) string {
	first, _, _ := strings.Cut(pattern, "/")
	return first
}

// covers reports whether a go test package argument (./internal/x or
// ./internal/x/...) includes directory dir.
func covers(pkg, dir string) bool {
	p := strings.TrimPrefix(pkg, "./")
	if p == "..." {
		return true
	}
	if prefix, ok := strings.CutSuffix(p, "/..."); ok {
		return dir == prefix || strings.HasPrefix(dir, prefix+"/")
	}
	return path.Clean(p) == dir
}

// selected returns the tests of the listing a run selects.
func (r goTestRun) selected(l map[key][]decl) ([]string, error) {
	re, err := regexp.Compile(topLevelPattern(r.pattern))
	if err != nil {
		return nil, err
	}
	var out []string
	for k := range l {
		if re.MatchString(k.Name) && r.coversDir(k.Dir) {
			out = append(out, k.Dir+" "+k.Name)
		}
	}
	return out, nil
}

func (r goTestRun) coversDir(dir string) bool {
	for _, p := range r.pkgs {
		if covers(p, dir) {
			return true
		}
	}
	return false
}
