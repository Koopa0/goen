package assets

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// The department photographs keep the 4:3 they are shot in, so a phone's
// directory is not as tall as it is wide, and they grow a little under a
// pointer instead of dimming. The zoom needs a hovering pointer and motion the
// reader has not turned off, and a press puts the photograph straight back.
func TestTheDepartmentPhotographsAreFourByThreeAndZoomOnHover(t *testing.T) {
	t.Parallel()

	sheet, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	css := string(sheet)

	well := regexp.MustCompile(`(?s)\n\.goen-cat__well \{([^}]*)\}`).FindStringSubmatch(css)
	if well == nil {
		t.Fatalf("%s has no .goen-cat__well rule", AppCSS)
	}
	for _, decl := range []string{"aspect-ratio: 4 / 3;", "border-radius: clamp(8px, 7cqi, 12px);"} {
		if !strings.Contains(well[1], decl) {
			t.Errorf(".goen-cat__well lacks %q", decl)
		}
	}

	if regexp.MustCompile(`(?s)\.goen-cat[^{]*__photo[^{]*\{[^}]*opacity`).MatchString(css) {
		t.Errorf("%s still dims a department photograph", AppCSS)
	}

	const hover = ".goen-cat:hover .goen-cat__photo"
	if n := strings.Count(css, hover); n != 1 {
		t.Fatalf("%s has %d rules for %q, want 1", AppCSS, n, hover)
	}
	guard := regexp.MustCompile(`(?s)@media \(hover: hover\) and \(prefers-reduced-motion: no-preference\) \{(.*?)\n\}\n`).FindAllStringSubmatch(css, -1)
	var zoom string
	for _, m := range guard {
		if strings.Contains(m[1], hover) {
			zoom = m[1]
		}
	}
	if zoom == "" {
		t.Fatalf("%s zooms the photograph outside (hover: hover) and (prefers-reduced-motion: no-preference)", AppCSS)
	}
	for name, rule := range map[string]string{
		"the photograph eases its scale with the shared move timing": `\.goen-cat__photo \{\s*transition: scale var\(--dur-move\) var\(--ease-move\);`,
		"a hover zooms by three percent":                             `\.goen-cat:hover \.goen-cat__photo \{\s*scale: 1\.03;`,
		"a press puts it back at once":                               `\.goen-cat:active \.goen-cat__photo \{\s*scale: 1;\s*transition: none;`,
	} {
		if !regexp.MustCompile(rule).MatchString(zoom) {
			t.Errorf("%s: the guarded block lacks the rule", name)
		}
	}
}
