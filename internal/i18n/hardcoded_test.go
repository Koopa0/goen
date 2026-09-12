package i18n

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// TestNoGenericChromeIsHardCoded refuses generic English UI words the code writes
// on a customer's screen without going through i18n. Exclusions are by CATEGORY
// with a reason, never string by string.
func TestNoGenericChromeIsHardCoded(t *testing.T) {
	t.Parallel()

	var offenders []string
	for path, src := range chromeSources(t) {
		if _, owed := pendingTranslation[path]; owed {
			continue
		}
		lines := strings.Split(src, "\n")
		for i, line := range lines {
			if hardCodedGeneric(path, line, before(lines, i)) {
				offenders = append(offenders, trimLine(path, i+1, line))
			}
		}
	}

	if len(offenders) > 0 {
		t.Errorf("%d hard-coded generic chrome strings on customer-facing surfaces. "+
			"Each is a label a zh-Hant visitor reads in English — move it to "+
			"internal/i18n, or mark the line // i18n-exempt: <why> if it must "+
			"stay as written:\n%s",
			len(offenders), strings.Join(offenders, "\n"))
	}
}

// TestNoChromeStringIsHardCoded refuses Han the code writes on a customer's
// screen. Exclusions are by CATEGORY with a reason, never string by string.
func TestNoChromeStringIsHardCoded(t *testing.T) {
	t.Parallel()

	var offenders []string
	for path, src := range chromeSources(t) {
		if _, owed := pendingTranslation[path]; owed {
			continue
		}
		lines := strings.Split(src, "\n")
		for i, line := range lines {
			if hardCodedHan(path, line, before(lines, i)) {
				offenders = append(offenders, trimLine(path, i+1, line))
			}
		}
	}

	if len(offenders) > 0 {
		t.Errorf("%d hard-coded Han strings on customer-facing surfaces. Each is a "+
			"string an English visitor reads in Chinese, and no catalogue test can "+
			"see it — move it to internal/i18n, or mark the line "+
			"// i18n-exempt: <why> if it must stay as written:\n%s",
			len(offenders), strings.Join(offenders, "\n"))
	}
}

// notChrome is every path prefix this test does not read, and why.
var notChrome = map[string]string{
	"internal/site/policies.go": "prose in Go; both languages declared per clause and guarded",
	"internal/i18n":             "the catalogue itself",
}

// pendingTranslation is DEBT, not decision: files that still write Chinese in
// code. The list may only shrink, and a translated file must leave it.
var pendingTranslation = map[string]struct{}{}

// chromeSources is every customer-facing Go, templ and SQL file under internal/
// and cmd/, by path.
func chromeSources(t *testing.T) map[string]string {
	t.Helper()

	out := map[string]string{}
	for _, root := range []string{"..", filepath.Join("..", "..", "cmd")} {
		collectSources(t, root, out)
	}
	if len(out) < 20 {
		t.Fatalf("found %d chrome files, want far more — the walk is not finding them", len(out))
	}
	return out
}

func collectSources(t *testing.T, root string, out map[string]string) {
	t.Helper()

	// Paths are reported relative to the repository root, so an exemption prefix
	// reads the same whichever tree the file came from.
	repoRoot, absErr := filepath.Abs(filepath.Join("..", ".."))
	if absErr != nil {
		t.Fatalf("resolve the repository root: %v", absErr)
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "db" {
				return filepath.SkipDir
			}
			return nil
		}
		abs, absPathErr := filepath.Abs(path)
		if absPathErr != nil {
			return absPathErr
		}
		rel, relErr := filepath.Rel(repoRoot, abs)
		if relErr != nil {
			return relErr
		}
		clean := filepath.ToSlash(filepath.Clean(rel))
		switch {
		case strings.HasSuffix(path, "_templ.go"):
			// Generated from the .templ beside it; reading both would let a
			// stale file speak.
			return nil
		case strings.HasSuffix(path, "_test.go"):
			// Fixture data, not chrome.
			return nil
		case !strings.HasSuffix(path, ".go") &&
			!strings.HasSuffix(path, ".templ") &&
			!strings.HasSuffix(path, ".sql"):
			return nil
		}
		for prefix := range notChrome {
			if strings.HasPrefix(clean, prefix) {
				return nil
			}
		}
		//nolint:gosec // G304: path comes from WalkDir over this repository, not a request
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		out[clean] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

// sqlStringLiteral matches a single-quoted SQL literal.
var sqlStringLiteral = regexp.MustCompile(`'((?:[^']|'')*)'`)

// beforeTrailingSQLComment cuts a line at its first -- that is not inside a literal.
func beforeTrailingSQLComment(line string) string {
	inLiteral := false
	for i := range len(line) - 1 {
		switch {
		case line[i] == '\'':
			inLiteral = !inLiteral
		case !inLiteral && line[i] == '-' && line[i+1] == '-':
			return line[:i]
		}
	}
	return line
}

// goStringLiteral matches a double-quoted Go or templ attribute string.
var goStringLiteral = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)

// hardCodedGeneric reports whether line puts a generic English UI word on a
// customer's screen; prev is the comment block above it, where an exemption may sit.
func hardCodedGeneric(path, line, prev string) bool {
	if !strings.HasSuffix(path, ".templ") {
		return false
	}
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "//"):
		return false
	case strings.Contains(line, "i18n-exempt:"), strings.Contains(prev, "i18n-exempt:"):
		return false
	case strings.Contains(line, "i18n.T"):
		return false
	}

	stripped := goStringLiteral.ReplaceAllString(line, "")
	if strings.Contains(stripped, ">Email<") || strings.Contains(stripped, ">Email<span") {
		return true
	}
	if strings.Contains(line, `"Email"`) {
		return true
	}
	return strings.Contains(line, `<th scope="col">English</th>`)
}

// hardCodedHan reports whether line puts Han characters on a customer's screen;
// prev is the comment block above it, where a long line's exemption may sit.
func hardCodedHan(path, line, prev string) bool {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "--"):
		// A SQL comment; the literal scan below cuts it anyway.
	case strings.HasPrefix(trimmed, "//"):
		return false
	case strings.Contains(line, "i18n-exempt:"), strings.Contains(prev, "i18n-exempt:"):
		return false
	}

	// SQL writes its words in SINGLE-quoted literals, and a query that
	// assembles chrome is chrome.
	if strings.HasSuffix(path, ".sql") {
		line = beforeTrailingSQLComment(line)
		for _, m := range sqlStringLiteral.FindAllStringSubmatch(line, -1) {
			if hasHan(m[1]) {
				return true
			}
		}
		return false
	}

	line = beforeTrailingComment(line)

	for _, m := range goStringLiteral.FindAllStringSubmatch(line, -1) {
		if hasHan(m[1]) {
			return true
		}
	}
	// templ writes body text outside quotes, so whatever Han is left once the
	// quoted parts are removed is text the browser will render.
	if strings.HasSuffix(path, ".templ") && hasHan(goStringLiteral.ReplaceAllString(line, "")) {
		return true
	}
	return false
}

// beforeTrailingComment returns line up to its comment, tracking quotes so a //
// inside a string does not truncate the code.
func beforeTrailingComment(line string) string {
	inString, escaped := false, false
	for i := range len(line) - 1 {
		switch {
		case escaped:
			escaped = false
		case line[i] == '\\' && inString:
			escaped = true
		case line[i] == '"':
			inString = !inString
		case !inString && line[i] == '/' && line[i+1] == '/':
			return line[:i]
		}
	}
	return line
}

// before is the comment block directly above line i, joined; it stops at the
// first line that is not a comment.
func before(lines []string, i int) string {
	var b strings.Builder
	for j := i - 1; j >= 0; j-- {
		t := strings.TrimSpace(lines[j])
		if !strings.HasPrefix(t, "//") {
			break
		}
		b.WriteString(t)
		b.WriteByte('\n')
	}
	return b.String()
}

func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// trimLine formats one finding, bounded so a wall of them stays readable.
func trimLine(path string, n int, line string) string {
	line = strings.TrimSpace(line)
	if len(line) > 90 {
		line = line[:90] + "…"
	}
	return "  " + path + ":" + itoa(n) + ": " + line
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// TestThePendingListOnlyHoldsRealDebt stops the list outliving the gap: a file
// left on it after translation is a file nothing checks any more.
func TestThePendingListOnlyHoldsRealDebt(t *testing.T) {
	t.Parallel()

	sources := chromeSources(t)
	for path := range pendingTranslation {
		src, found := sources[path]
		if !found {
			t.Errorf("%s is listed as pending translation and is not a chrome source — "+
				"it was renamed, deleted, or excluded by category. Drop the entry.", path)
			continue
		}
		owed := false
		lines := strings.Split(src, "\n")
		for i, line := range lines {
			if hardCodedHan(path, line, before(lines, i)) {
				owed = true
				break
			}
		}
		if !owed {
			t.Errorf("%s has no hard-coded Han left. Remove it from pendingTranslation "+
				"so the guard starts watching it.", path)
		}
	}
}
