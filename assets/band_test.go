package assets

import (
	"io/fs"
	"strings"
	"testing"
)

// TestAppCSSBandNameUpright checks the storefront's one exception to WCAG
// 1.4.10 and 1.4.12, a department's name set upright (E2 in the design notes):
// writing-mode is declared once in all three stylesheets, on the band's name in
// Chinese, inside a query of the band's column in rem, so that larger text, a
// zoom or a narrow column lays the name down again. The band itself is square.
func TestAppCSSBandNameUpright(t *testing.T) {
	t.Parallel()

	var upright []string
	for _, name := range []string{BaseCSS, AppCSS, AdminCSS} {
		body, err := fs.ReadFile(files, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, d := range cssDeclarations(string(body)) {
			if strings.HasPrefix(d.decl, "writing-mode") {
				upright = append(upright, name+": "+strings.Join(append(d.atRules, d.selector), " ")+" { "+d.decl+" }")
				if name != AppCSS || d.selector != ".goen-band__name:lang(zh)" || d.decl != "writing-mode: vertical-rl" ||
					len(d.atRules) != 1 || d.atRules[0] != "@container shelf (width >= 64rem)" {
					t.Errorf("writing-mode outside the band's name in a 64rem column: %s", upright[len(upright)-1])
				}
			}
			if name == AppCSS && (d.selector == ".goen-band" || strings.HasPrefix(d.selector, ".goen-band__")) && strings.HasPrefix(d.decl, "border-radius") {
				t.Errorf("%s rounds the band: %s { %s }", AppCSS, d.selector, d.decl)
			}
		}
	}
	if len(upright) != 1 {
		t.Errorf("writing-mode is declared %d times, want once, on the band's name: %q", len(upright), upright)
	}
}
