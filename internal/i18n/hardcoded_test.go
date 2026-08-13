package i18n

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// TestNoChromeStringIsHardCoded is the locale rule from the other side.
//
// Three tests already police the catalogue: every key is translated, the English
// is actually English, and every key is rendered by something. All three ask
// questions ABOUT THE CATALOGUE, and none of them can see the failure that
// actually keeps happening — a string that never reached the catalogue at all.
//
// That is how the cart, the checkout and the buy panel stayed hard-coded Chinese
// while their translations sat unused: adding a page is the moment somebody
// writes copy, and nothing was watching the page.
//
// # What counts as chrome
//
// Everything the CODE writes on a customer's screen. What is excluded is
// excluded by CATEGORY with a reason, never string by string — a per-string
// allowlist grows to the size of the debt and then says nothing.
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
//
// Each entry is a DECISION. A reader disagreeing with one should argue with the
// reason rather than discover the omission.
var notChrome = map[string]string{
	// The back office USED TO BE HERE, on the grounds that it serves the staff of
	// one Taiwanese shop. That was a decision about who reads it, and the owner
	// has changed the answer: goen's chrome is bilingual everywhere the code
	// writes it, /admin included. The entry even named its own trigger — "if goen
	// ever hires somebody who does not read Chinese, this line is what has to
	// change first" — so it is DELETED rather than narrowed, and the 53 files it
	// covered are in pendingTranslation below, where a list that can only shrink
	// is watching them.
	//
	// Prose in Go rather than in the catalogue, because a policy document changes
	// when a LAWYER changes it and a lookup table is not where that belongs —
	// paragraphs are also the wrong shape for key(), which exists for short
	// chrome.
	//
	// The reason used to end "translated editorially or not at all", and that was
	// wrong by CLAUDE.md's own test: copy compiled into the binary is goen's to
	// say in both languages, and only copy typed into a TABLE is the shop's to
	// say however it likes. It stopped being untidy when /returns began stating
	// 消保法 §19, which an English-reading customer in Taiwan holds identically.
	// Both languages are declared side by side on each clause now, and
	// TestEveryPolicyClauseIsTranslated refuses a half-translated document — so
	// this entry excuses the Han LITERALS here, not the absence of English.
	"internal/site/policies.go": "prose in Go; both languages declared per clause and guarded",
	// This package IS the catalogue.
	"internal/i18n": "the catalogue itself",
}

// pendingTranslation is DEBT, not decision.
//
// Every entry is a customer-facing file that still writes Chinese in code, named
// so the gap is a list somebody can work through rather than a number in a test
// output. It exists because the migration is larger than one commit: 818 strings
// across the storefront, the account pages and the site pages.
//
// Two rules, and TestThePendingListOnlyHoldsRealDebt holds the second:
//
//   - the list may only SHRINK. Adding a file to it is adding a page an English
//     visitor cannot read, which is the thing this test exists to refuse.
//   - a file that has been migrated must LEAVE it. Otherwise the list stops
//     describing the debt and starts hiding new work — a stale entry is worse
//     than no entry, because it reads as covered.
//
// Distinct from notChrome above, which is a set of decisions with reasons. These
// have no reason beyond "not done yet".
//
// The list was EMPTY, and it is full again for a reason worth writing down: the
// storefront migration finished and the back office was never in it, excused by
// a notChrome entry that named the audience. The owner has changed the audience,
// so 1,058 strings across 52 files became debt in one commit — which is what this
// mechanism is for, and the honest alternative to a half-migrated tree where some
// admin pages answer to a locale and some do not.
//
// The expensive half is not the strings. It is that a back-office view model
// computes its words in a method with no request to read a locale from —
// StatusLabel, ReasonText, the audit trail's action map — so each becomes either
// a method returning an i18n.Key or one taking a ctx, per the two patterns
// CLAUDE.md records. Every file that leaves this list takes its share of that
// with it.
var pendingTranslation = map[string]struct{}{
	"internal/admin/banner.go":                {},
	"internal/admin/campaign.go":              {},
	"internal/admin/coupon.go":                {},
	"internal/admin/faq.go":                   {},
	"internal/admin/hero.go":                  {},
	"internal/admin/product.go":               {},
	"internal/admin/shipping.go":              {},
	"internal/admin/query.sql":                {},
	"internal/admin/taxonomy.go":              {},
	"internal/ui/pages/admin.go":              {},
	"internal/ui/pages/admin.templ":           {},
	"internal/ui/pages/admincampaign.go":      {},
	"internal/ui/pages/admincampaign.templ":   {},
	"internal/ui/pages/admincoupon.go":        {},
	"internal/ui/pages/admincoupon.templ":     {},
	"internal/ui/pages/admincredit.templ":     {},
	"internal/ui/pages/admincustomer.templ":   {},
	"internal/ui/pages/adminfaq.templ":        {},
	"internal/ui/pages/adminhealth.templ":     {},
	"internal/ui/pages/adminhome.templ":       {},
	"internal/ui/pages/adminmessage.go":       {},
	"internal/ui/pages/adminmessage.templ":    {},
	"internal/ui/pages/adminmovements.templ":  {},
	"internal/ui/pages/adminnewsletter.templ": {},
	"internal/ui/pages/adminproduct.go":       {},
	"internal/ui/pages/adminproduct.templ":    {},
	"internal/ui/pages/adminquestion.go":      {},
	"internal/ui/pages/adminquestion.templ":   {},
	"internal/ui/pages/adminreport.go":        {},
	"internal/ui/pages/adminreport.templ":     {},
	"internal/ui/pages/adminreturns.templ":    {},
	"internal/ui/pages/adminreview.go":        {},
	"internal/ui/pages/adminreview.templ":     {},
	"internal/ui/pages/adminshipping.go":      {},
	"internal/ui/pages/adminshipping.templ":   {},
	"internal/ui/pages/adminstaff.go":         {},
	"internal/ui/pages/adminstaff.templ":      {},
	"internal/ui/pages/admintaxonomy.go":      {},
	"internal/ui/pages/admintaxonomy.templ":   {},
	"internal/ui/pages/admintiers.templ":      {},
	"internal/ui/pages/adminwarranty.templ":   {},
	"internal/ui/pages/audit.templ":           {},
	"internal/ui/pages/twofactor.templ":       {},
	"internal/ui/pages/workerhealth.go":       {},
}

// chromeSources is every customer-facing Go and templ file, by path.
//
// internal/ AND cmd/. The walk read only internal/ to begin with, and cmd/goen
// answers requests: the panic recovery's status line is a sentence a customer
// reads, and it sat outside every question this file asks. A blind spot in a
// sweep is worse than a gap in it — the sweep reports clean either way, and the
// one string in there had no exemption comment because nothing ever asked it for
// one.
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

// collectSources reads one tree into out.
func collectSources(t *testing.T, root string, out map[string]string) {
	t.Helper()

	// Paths are reported relative to the repository root, so an exemption prefix
	// reads the same whichever tree the file came from. Absolute, because the two
	// roots are reached by different numbers of `..` and filepath.Rel cannot
	// compare those.
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
			// Generated from the .templ beside it. Reading both would report
			// every finding twice and let a stale file speak.
			return nil
		case strings.HasSuffix(path, "_test.go"):
			// Fixture data. A test that seeds a product called 手機 is not
			// writing chrome, and demanding a key for it would put the test's
			// subject in the catalogue.
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

// beforeTrailingSQLComment cuts a line at its first -- that is not inside a
// literal. A query's explanation sits beside it as often as above it.
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

// hardCodedHan reports whether line puts Han characters on a customer's screen.
//
// prev is the comment block immediately above it, which is where a long line's
// exemption goes — the same placement //nolint accepts, and for the same reason:
// a reason worth reading does not fit beside 80 characters of Chinese.
func hardCodedHan(path, line, prev string) bool {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "--"):
		// A SQL comment. Every query file in this repository explains itself in
		// prose and much of that prose is Chinese; none of it reaches a browser.
	case strings.HasPrefix(trimmed, "//"):
		// A comment. Chinese in a comment explains the code to whoever reads it
		// next, and nobody sees it in a browser.
		return false
	case strings.Contains(line, "i18n-exempt:"), strings.Contains(prev, "i18n-exempt:"):
		// Named, with a reason, on the line or in the comment above it. The
		// escape has to exist: Locale.Label returns 繁體中文 in BOTH locales on
		// purpose — somebody looking for their own language should not have to
		// read another one to find it.
		return false
	}

	// SQL is scanned for SINGLE-quoted literals, which is where a query writes
	// words: 'cancelled' is a value, '顧客自行取消' was a sentence a customer read
	// off their own order page. Two defects lived in .sql files precisely because
	// this sweep read .go and .templ — a query that assembles chrome is chrome
	// written where no locale exists, and the file type was the whole hiding place.
	if strings.HasSuffix(path, ".sql") {
		line = beforeTrailingSQLComment(line)
		for _, m := range sqlStringLiteral.FindAllStringSubmatch(line, -1) {
			if hasHan(m[1]) {
				return true
			}
		}
		return false
	}

	// A trailing comment is still a comment. `Label string // "星霧藍 · 512GB"`
	// documents the field with an example, and demanding a catalogue key for it
	// would be the test failing to read Go. Cut at the first // that is not
	// inside a string, which is what any comment on a line of code is.
	line = beforeTrailingComment(line)

	for _, m := range goStringLiteral.FindAllStringSubmatch(line, -1) {
		if hasHan(m[1]) {
			return true
		}
	}
	// templ writes body text outside quotes, so the quoted parts are removed and
	// whatever Han is left is text the browser will render.
	if strings.HasSuffix(path, ".templ") && hasHan(goStringLiteral.ReplaceAllString(line, "")) {
		return true
	}
	return false
}

// beforeTrailingComment returns line up to its comment, tracking quotes so a //
// inside a string — a URL path, an hx-target — does not truncate the code.
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

// before is the comment block directly above line i, joined. It stops at the
// first line that is not a comment, so an exemption three functions up cannot
// reach down and silence something.
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

// TestThePendingListOnlyHoldsRealDebt stops the exemption list outliving the gap.
//
// A file that has been translated and left in pendingTranslation is a file
// nothing checks any more. That is exactly how an allowlist turns from a plan
// into cover, and it is silent — the suite stays green while the guard shrinks.
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
