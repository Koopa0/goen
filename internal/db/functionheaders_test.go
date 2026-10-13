package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// migrationText is one migration file as the header checker reads it.
type migrationText struct {
	name string
	text string
}

var (
	// createFunctionStmt finds the start of a function or procedure definition.
	// It is anchored to a line start so a word inside a string literal in a body
	// is not read as a definition; comments are blanked before it runs.
	createFunctionStmt = regexp.MustCompile(
		`(?im)^[ \t]*CREATE[ \t]+(?:OR[ \t]+REPLACE[ \t]+)?(?:FUNCTION|PROCEDURE)[ \t]+([A-Za-z0-9_."]+)[ \t]*\(`)
	// alterFunctionSecurity is a property changed outside the function's header.
	alterFunctionSecurity = regexp.MustCompile(
		`(?is)\bALTER\s+(?:FUNCTION|PROCEDURE)\b[^;]*?\bSECURITY\s+(?:DEFINER|INVOKER)\b`)
	headerSecurityDefiner = regexp.MustCompile(`(?i)\bSECURITY DEFINER\b`)
	headerPinnedPath      = regexp.MustCompile(`(?i)\bSET search_path = pg_catalog, public, pg_temp(?:\s|$)`)
	whitespaceRun         = regexp.MustCompile(`\s+`)
)

// requiredSearchPath is the one search_path a function header must state.
const requiredSearchPath = "SET search_path = pg_catalog, public, pg_temp"

// minimumFunctionHeaders is how many definitions the shipped migrations hold. The
// real-file test fails below it, so a parser that stops matching cannot pass by
// finding nothing.
const minimumFunctionHeaders = 125

// TestMigrationFunctionHeadersStateTheirProperties reads the shipped migrations
// in order. A function is re-created in a later migration with CREATE OR
// REPLACE, which resets SECURITY DEFINER and the pinned search_path to their
// defaults when the new header leaves them out, so each header has to carry
// both itself.
func TestMigrationFunctionHeadersStateTheirProperties(t *testing.T) {
	t.Parallel()

	paths, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no migrations/*.up.sql found; the header check has nothing to read")
	}

	files := make([]migrationText, 0, len(paths))
	for _, path := range paths { // Glob returns lexical order, which is migration order.
		src, readErr := os.ReadFile(path) //nolint:gosec // fixed test workspace path, never user input
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		files = append(files, migrationText{name: filepath.Base(path), text: string(src)})
	}

	headers := 0
	for _, f := range files {
		headers += len(createFunctionStmt.FindAllStringIndex(blankSQLComments(f.text), -1))
	}
	if headers < minimumFunctionHeaders {
		t.Fatalf("found %d function headers, want at least %d; the parser is not reading the migrations",
			headers, minimumFunctionHeaders)
	}

	for _, finding := range checkFunctionHeaders(files) {
		t.Error(finding)
	}
}

// TestFunctionHeaderCheckerFixtures proves the checker can fail: each fixture
// is a defect it exists to catch, and the clean ones must pass.
func TestFunctionHeaderCheckerFixtures(t *testing.T) {
	t.Parallel()

	const definer = "CREATE FUNCTION f() RETURNS int\nLANGUAGE sql SECURITY DEFINER " + requiredSearchPath + " AS $$ SELECT 1 $$;\n"
	const invoker = "CREATE OR REPLACE FUNCTION f() RETURNS int\nLANGUAGE sql " + requiredSearchPath + " AS $$ SELECT 1 $$;\n"

	tests := []struct {
		name  string
		files []migrationText
		want  string // a substring some finding must contain; empty means no findings at all
	}{
		{
			name:  "a later file re-creates a definer without SECURITY DEFINER",
			files: []migrationText{{"001.up.sql", definer}, {"002.up.sql", invoker}},
			want:  "002.up.sql:1: f() was SECURITY DEFINER",
		},
		{
			name:  "a later file re-creates a definer and restates it",
			files: []migrationText{{"001.up.sql", definer}, {"002.up.sql", strings.Replace(invoker, "LANGUAGE sql ", "LANGUAGE sql SECURITY DEFINER ", 1)}},
		},
		{
			name:  "a header without the pinned search_path",
			files: []migrationText{{"001.up.sql", "CREATE FUNCTION g() RETURNS int LANGUAGE sql AS $$ SELECT 1 $$;\n"}},
			want:  "001.up.sql:1: g() header does not state",
		},
		{
			name:  "the old two-element search_path",
			files: []migrationText{{"001.up.sql", "CREATE FUNCTION g() RETURNS int LANGUAGE sql SET search_path = public, pg_temp AS $$ SELECT 1 $$;\n"}},
			want:  "001.up.sql:1: g() header does not state",
		},
		{
			name:  "a longer search_path does not satisfy the exact one",
			files: []migrationText{{"001.up.sql", "CREATE FUNCTION g() RETURNS int LANGUAGE sql SET search_path = pg_catalog, public, pg_temp, extra AS $$ SELECT 1 $$;\n"}},
			want:  "001.up.sql:1: g() header does not state",
		},
		{
			name:  "a procedure is held to the same rule",
			files: []migrationText{{"001.up.sql", "CREATE PROCEDURE p() LANGUAGE sql AS $$ SELECT 1 $$;\n"}},
			want:  "001.up.sql:1: p() header does not state",
		},
		{
			name:  "a property changed by ALTER FUNCTION",
			files: []migrationText{{"001.up.sql", definer + "ALTER FUNCTION f() SECURITY DEFINER;\n"}},
			want:  "001.up.sql:3: ALTER FUNCTION",
		},
		{
			name:  "ALTER FUNCTION SECURITY INVOKER over two lines is refused too",
			files: []migrationText{{"001.up.sql", definer + "ALTER FUNCTION f()\n    SECURITY INVOKER;\n"}},
			want:  "001.up.sql:3: ALTER FUNCTION",
		},
		{
			name:  "ALTER FUNCTION that only sets search_path is not a property change",
			files: []migrationText{{"001.up.sql", definer + "ALTER FUNCTION f() " + requiredSearchPath + ";\n"}},
		},
		{
			name:  "commented-out definitions and ALTERs are ignored",
			files: []migrationText{{"001.up.sql", "-- CREATE FUNCTION h() RETURNS int LANGUAGE sql AS $$ SELECT 1 $$;\n-- ALTER FUNCTION f() SECURITY DEFINER;\n" + definer}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := checkFunctionHeaders(tt.files)
			if tt.want == "" {
				if len(got) != 0 {
					t.Fatalf("clean fixture reported findings: %q", got)
				}
				return
			}
			for _, finding := range got {
				if strings.Contains(finding, tt.want) {
					return
				}
			}
			t.Fatalf("no finding contains %q; got %q", tt.want, got)
		})
	}
}

// checkFunctionHeaders returns one finding per violation, in file order.
func checkFunctionHeaders(files []migrationText) []string {
	var findings []string
	wasDefiner := map[string]bool{} // function name -> its most recent header said SECURITY DEFINER

	for _, f := range files {
		src := blankSQLComments(f.text)

		for _, m := range alterFunctionSecurity.FindAllStringIndex(src, -1) {
			findings = append(findings, f.name+":"+migrationLine(src, m[0])+
				": ALTER FUNCTION sets SECURITY DEFINER or INVOKER; a property lives in the function header only")
		}

		for _, m := range createFunctionStmt.FindAllStringSubmatchIndex(src, -1) {
			name := strings.ToLower(strings.NewReplacer(`"`, "", "public.", "").Replace(src[m[2]:m[3]])) + "()"
			at := f.name + ":" + migrationLine(src, m[0])
			end := strings.Index(src[m[0]:], "AS $")
			if end < 0 {
				findings = append(findings, at+": "+name+" has no AS $ body marker; the checker cannot find its header")
				continue
			}
			header := strings.TrimSpace(whitespaceRun.ReplaceAllString(src[m[0]:m[0]+end], " "))

			if !headerPinnedPath.MatchString(header) {
				findings = append(findings, at+": "+name+" header does not state "+requiredSearchPath)
			}
			isDefiner := headerSecurityDefiner.MatchString(header)
			if wasDefiner[name] && !isDefiner {
				findings = append(findings, at+": "+name+
					" was SECURITY DEFINER and its re-created header does not say so; CREATE OR REPLACE would silently make it SECURITY INVOKER")
			}
			wasDefiner[name] = isDefiner
		}
	}
	return findings
}

// blankSQLComments replaces -- and /* */ comments with spaces, keeping every
// newline and byte offset so line numbers stay true.
func blankSQLComments(src string) string {
	b := []byte(src)
	for i := 0; i < len(b); i++ {
		switch {
		case b[i] == '-' && i+1 < len(b) && b[i+1] == '-':
			for ; i < len(b) && b[i] != '\n'; i++ {
				b[i] = ' '
			}
		case b[i] == '/' && i+1 < len(b) && b[i+1] == '*':
			for ; i < len(b) && !(b[i] == '*' && i+1 < len(b) && b[i+1] == '/'); i++ {
				if b[i] != '\n' {
					b[i] = ' '
				}
			}
			if i+1 < len(b) {
				b[i], b[i+1] = ' ', ' '
				i++
			}
		}
	}
	return string(b)
}

func migrationLine(src string, offset int) string {
	return strconv.Itoa(1 + strings.Count(src[:offset], "\n"))
}
