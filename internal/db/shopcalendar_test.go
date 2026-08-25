package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var (
	calendarCurrentDate = regexp.MustCompile(`(?i)\bcurrent_date\b`)
	calendarAtDate      = regexp.MustCompile(`(?i)\b(?:[a-z_][a-z0-9_]*\.)?(?:[a-z_][a-z0-9_]*_at|at)\s*::\s*date\b`)
	calendarTaipei      = regexp.MustCompile(`(?i)\bAT\s+TIME\s+ZONE\s+'Asia/Taipei'`)
	shopDayDefinition   = regexp.MustCompile(`(?is)\bCREATE\s+FUNCTION\s+shop_day\s*\([^)]*\).*?\bAS\s+\$\$.*?\$\$\s*;`)
	queryPathInSQLC     = regexp.MustCompile(`(?m)^\s*-\s*["']([^"']+/query\.sql)["']\s*$`)
)

// TestEveryCalendarDayGoesThroughTheShopsCalendar holds shop_day/shop_today as
// the one definition of the shop's calendar. It deliberately catches only a
// timestamptz cast whose identifier is named `at` or ends `_at`; a timestamp
// column named otherwise is beyond what a source census can infer.
func TestEveryCalendarDayGoesThroughTheShopsCalendar(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	paths := calendarSQLFiles(t, root)
	// There are no exemptions when this guard lands. If one is ever necessary,
	// it is an exact file:line:rule identity with a reason, never a count.
	allowed := map[string]string{}
	used := map[string]bool{}

	for _, path := range paths {
		srcBytes, err := os.ReadFile(path) //nolint:gosec // paths come from sqlc.yaml or the migration glob
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		src := string(srcBytes)
		withoutComments := maskCalendarSQL(src, false)
		withoutCommentsOrStrings := maskCalendarSQL(src, true)
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		for _, match := range calendarCurrentDate.FindAllStringIndex(withoutCommentsOrStrings, -1) {
			reportCalendarFinding(t, allowed, used, rel, src, match[0], "current_date",
				"current_date answers in the session TimeZone; use shop_today()")
		}
		for _, match := range calendarAtDate.FindAllStringIndex(withoutCommentsOrStrings, -1) {
			reportCalendarFinding(t, allowed, used, rel, src, match[0], "timestamptz-date-cast",
				"a timestamptz ::date answers in the session TimeZone; use shop_day()")
		}

		definition := shopDayDefinition.FindStringIndex(withoutComments)
		for _, match := range calendarTaipei.FindAllStringIndex(withoutComments, -1) {
			if len(definition) == 2 && match[0] >= definition[0] && match[1] <= definition[1] {
				continue
			}
			reportCalendarFinding(t, allowed, used, rel, src, match[0], "taipei-literal",
				"Asia/Taipei belongs only in shop_day(); a second literal is calendar drift")
		}
	}

	for id, why := range allowed {
		if !used[id] {
			t.Errorf("the shop-calendar allowlist exempts %q (%s), but that exact finding no longer exists", id, why)
		}
	}
}

func calendarSQLFiles(t *testing.T, root string) []string {
	t.Helper()

	configPath := filepath.Join(root, "sqlc.yaml")
	config, err := os.ReadFile(configPath) //nolint:gosec // root is the fixed test workspace, never user input
	if err != nil {
		t.Fatalf("read sqlc.yaml: %v", err)
	}
	queryMatches := queryPathInSQLC.FindAllStringSubmatch(string(config), -1)
	if len(queryMatches) < 18 {
		t.Fatalf("sqlc.yaml named only %d query.sql files, want at least 18; the corpus parser is stale", len(queryMatches))
	}

	paths := make([]string, 0, len(queryMatches)+2)
	for _, match := range queryMatches {
		paths = append(paths, filepath.Join(root, filepath.FromSlash(match[1])))
	}
	migrations, err := filepath.Glob(filepath.Join(root, "migrations", "*.up.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("no migrations/*.up.sql found; the calendar census has no schema corpus")
	}
	paths = append(paths, migrations...)
	sort.Strings(paths)
	return paths
}

func reportCalendarFinding(
	t *testing.T,
	allowed map[string]string,
	used map[string]bool,
	path, src string,
	offset int,
	rule, message string,
) {
	t.Helper()
	line := 1 + strings.Count(src[:offset], "\n")
	id := path + ":" + strconv.Itoa(line) + ":" + rule
	if _, ok := allowed[id]; ok {
		used[id] = true
		return
	}
	t.Errorf("%s:%d: %s (%s)", path, line, message, rule)
}

// maskCalendarSQL preserves byte offsets and newlines while replacing comments,
// and optionally ordinary single-quoted strings, with spaces. Dollar-quoted
// function bodies remain code: that is where the migration's calendar rules live.
func maskCalendarSQL(src string, maskStrings bool) string {
	const (
		calendarCode = iota
		calendarLineComment
		calendarBlockComment
		calendarString
	)

	out := []byte(src)
	state := calendarCode
	blockDepth := 0
	for i := 0; i < len(src); i++ {
		switch state {
		case calendarCode:
			switch {
			case i+1 < len(src) && src[i:i+2] == "--":
				out[i], out[i+1] = ' ', ' '
				i++
				state = calendarLineComment
			case i+1 < len(src) && src[i:i+2] == "/*":
				out[i], out[i+1] = ' ', ' '
				i++
				blockDepth = 1
				state = calendarBlockComment
			case src[i] == '\'':
				if maskStrings {
					out[i] = ' '
				}
				state = calendarString
			}
		case calendarLineComment:
			if src[i] == '\n' {
				state = calendarCode
			} else {
				out[i] = ' '
			}
		case calendarBlockComment:
			switch {
			case src[i] == '\n':
			case i+1 < len(src) && src[i:i+2] == "/*":
				out[i], out[i+1] = ' ', ' '
				i++
				blockDepth++
			case i+1 < len(src) && src[i:i+2] == "*/":
				out[i], out[i+1] = ' ', ' '
				i++
				blockDepth--
				if blockDepth == 0 {
					state = calendarCode
				}
			default:
				out[i] = ' '
			}
		case calendarString:
			if src[i] == '\n' {
				if maskStrings {
					// Preserve the newline so diagnostics keep the source line.
					out[i] = '\n'
				}
				continue
			}
			if src[i] != '\'' {
				if maskStrings {
					out[i] = ' '
				}
				continue
			}
			if i+1 < len(src) && src[i+1] == '\'' {
				if maskStrings {
					out[i], out[i+1] = ' ', ' '
				}
				i++
				continue
			}
			if maskStrings {
				out[i] = ' '
			}
			state = calendarCode
		}
	}
	return string(out)
}
