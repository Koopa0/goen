package assets

import (
	"io/fs"
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

// TestAppCSSNoUprightText checks that no stylesheet sets text upright. The one
// exception to WCAG 1.4.10 and 1.4.12 the design notes granted (E2), a
// department's name standing in a band, went with the band; a writing-mode
// declared anywhere now is a column that does not reflow.
func TestAppCSSNoUprightText(t *testing.T) {
	t.Parallel()

	for _, name := range []string{BaseCSS, AppCSS, AdminCSS} {
		body, err := fs.ReadFile(files, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, d := range cssDeclarations(string(body)) {
			if strings.HasPrefix(d.decl, "writing-mode") {
				t.Errorf("%s sets text upright: %s { %s }", name, strings.Join(append(d.atRules, d.selector), " "), d.decl)
			}
		}
	}
}
