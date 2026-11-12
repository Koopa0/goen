package assets

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// cssScope is one declaration with the selector it is in and the at-rules
// around it, outermost first.
type cssScope struct {
	atRules  []string
	selector string
	decl     string
}

// cssDeclarations walks a sheet with its comments taken out and returns every
// declaration with its selector and the at-rules it sits in, so a rule can be
// read where it applies and not only where it is written.
func cssDeclarations(sheet string) []cssScope {
	sheet = cssComment.ReplaceAllString(sheet, "")
	var out []cssScope
	var stack []string
	start := 0
	for i := range len(sheet) {
		switch sheet[i] {
		case '{':
			stack = append(stack, strings.Join(strings.Fields(sheet[start:i]), " "))
			start = i + 1
		case ';', '}':
			if decl := strings.TrimSpace(sheet[start:i]); decl != "" && len(stack) > 0 && !strings.HasPrefix(stack[len(stack)-1], "@") {
				out = append(out, cssScope{atRules: append([]string(nil), stack[:len(stack)-1]...), selector: stack[len(stack)-1], decl: decl})
			}
			if sheet[i] == '}' && len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			start = i + 1
		}
	}
	return out
}

// TestAppCSSHeroPhotographSquare checks that the hero is a printed page: its
// photograph square and across the window, its name in the display size in
// every language, the end and the days in vermilion, and the English end's
// first letter raised, since the phrase is written to follow words.
func TestAppCSSHeroPhotographSquare(t *testing.T) {
	t.Parallel()

	body, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	sheet := cssComment.ReplaceAllString(string(body), "")
	for name, rule := range map[string]string{
		"the photograph is square":            `(?s)\n\.goen-hero__media \{[^}]*\bborder-radius: 0;`,
		"the photograph spans the hero":       `(?s)\n\.goen-hero__media img \{[^}]*\bwidth: 100%;`,
		"the name is the display size":        `(?s)\n\.goen-hero__title \{[^}]*\bfont: var\(--type-display-l\);`,
		"the end is vermilion":                `(?s)\n\.goen-hero__ends \{\s*color: var\(--sys-tertiary\);`,
		"the days to come are vermilion":      `(?s)\n\.goen-days > i \{\s*background: var\(--sys-tertiary\);`,
		"the days gone are a quiet line":      `(?s)\n\.goen-days > i\[data-cell="past"\] \{\s*background: var\(--sys-outline-variant\);`,
		"the English end starts with capital": `(?s)\.goen-hero__ends::first-letter,?[^{]*\{\s*text-transform: uppercase;`,
	} {
		if !regexp.MustCompile(rule).MatchString(sheet) {
			t.Errorf("%s: %s lacks the rule %s", name, AppCSS, rule)
		}
	}
	// Nothing rounds the photograph or its picture, and no language sets the
	// name in another size.
	for _, d := range cssDeclarations(string(body)) {
		if strings.Contains(d.selector, "goen-hero__media") && strings.HasPrefix(d.decl, "border-radius") && d.decl != "border-radius: 0" {
			t.Errorf("%s rounds the hero's photograph: %s { %s }", AppCSS, d.selector, d.decl)
		}
		if strings.Contains(d.selector, "goen-hero__title:lang") && regexp.MustCompile(`^font(-size)?:`).MatchString(d.decl) {
			t.Errorf("%s sets the hero's name in another size for one language: %s { %s }", AppCSS, d.selector, d.decl)
		}
	}
}
