package assets

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// Anything that can be pressed is a pill, and the corner is one token in rem so
// it grows with the text. The token lives in the storefront's :root only:
// base.css keeps the --r-* values the back office is drawn with.
func TestPressableControlsShareOnePillCorner(t *testing.T) {
	t.Parallel()

	read := func(name string) string {
		t.Helper()
		sheet, err := fs.ReadFile(files, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(sheet)
	}
	app, base, admin := read(AppCSS), read(BaseCSS), read(AdminCSS)

	declaration := regexp.MustCompile(`--r-press:`)
	if n := len(declaration.FindAllString(app, -1)); n != 1 {
		t.Fatalf("%s declares --r-press %d times, want once", AppCSS, n)
	}
	if !regexp.MustCompile(`(?m)^:root \{\n(?: {2}[^\n]*\n|\n)*? {2}--r-press: 1\.625rem;\n`).MatchString(app) {
		t.Errorf("%s does not declare --r-press: 1.625rem inside a top-level :root", AppCSS)
	}
	for name, sheet := range map[string]string{BaseCSS: base, AdminCSS: admin} {
		if declaration.MatchString(sheet) {
			t.Errorf("%s declares --r-press; the token belongs to %s", name, AppCSS)
		}
	}

	for _, radius := range []string{
		"--r-sm: 0.25rem;",
		"--r-md: 0.375rem;",
		"--r-lg: 0.5rem;",
		"--r-xl: 0.5rem;",
		"--r-full: 999px;",
	} {
		if strings.Count(base, radius) != 1 {
			t.Errorf("%s lost %q, which the back office is drawn with", BaseCSS, radius)
		}
	}

	rule := func(selector string) string {
		t.Helper()
		matches := regexp.MustCompile(`(?m)^`+regexp.QuoteMeta(selector)+` \{\n([^}]*)\}`).FindAllStringSubmatch(app, -1)
		if len(matches) != 1 {
			t.Fatalf("%s has %d top-level rules for %q, want 1", AppCSS, len(matches), selector)
		}
		return matches[0][1]
	}
	radius := func(selector string) string {
		t.Helper()
		found := regexp.MustCompile(`(?m)^ {2}border-radius: ([^;]*);$`).FindAllStringSubmatch(rule(selector), -1)
		if len(found) != 1 {
			t.Fatalf("%q declares border-radius %d times, want once", selector, len(found))
		}
		return found[0][1]
	}

	for _, selector := range []string{
		".goen-btn",
		".goen-stepper",
		".goen-langmenu > summary",
		".goen-filters__chip",
		".goen-header__action--wide,\n.goen-header__backoffice",
		".goen-footer__field > .goen-input",
	} {
		if got := radius(selector); got != "var(--r-press)" {
			t.Errorf("%q has border-radius %s, want var(--r-press)", selector, got)
		}
	}
	// The heart is a circle, the signup button sits 4px inside a field whose
	// own corner is --r-press, and the hero arrows are circles.
	for selector, want := range map[string]string{
		".goen-header__action--wide":      "50%",
		".goen-footer__field > .goen-btn": "calc(var(--r-press) - 4px)",
		".goen-hero__arrow":               "50%",
	} {
		if got := radius(selector); got != want {
			t.Errorf("%q has border-radius %s, want %s", selector, got, want)
		}
	}

	// No shadow on a resting control: the filled button is the blue alone.
	if strings.Contains(rule(".goen-btn--primary"), "box-shadow") {
		t.Errorf(".goen-btn--primary draws a shadow")
	}

	// A hero arrow is a tonal circle: forced colours draw the transparent border.
	arrow := rule(".goen-hero__arrow")
	for _, decl := range []string{"background: var(--n-100);", "border: 1px solid transparent;"} {
		if !strings.Contains(arrow, decl) {
			t.Errorf(".goen-hero__arrow lacks %q", decl)
		}
	}
}
