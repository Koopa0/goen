package assets

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestAppCSSCartMeterAsDrawn checks the free-delivery meter's bar, a native
// progress element, as the board draws it: its own look switched off so the
// track and the fill can be drawn; the fill drawn in each engine's own
// pseudo-element, the cobalt until the threshold is met and the green after;
// and, in forced colours, an edge on the track and the system's highlight for
// the fill. A rule that names the WebKit and the Firefox pseudo-element
// together is dropped by both, so no rule may group them.
func TestAppCSSCartMeterAsDrawn(t *testing.T) {
	t.Parallel()

	body, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	sheet := cssComment.ReplaceAllString(string(body), "")
	declared := func(sheet, selector string) []string {
		var blocks []string
		for _, rule := range cssRule.FindAllStringSubmatch(sheet, -1) {
			for s := range strings.SplitSeq(rule[1], ",") {
				if strings.Join(strings.Fields(s), " ") == selector {
					blocks = append(blocks, rule[2])
				}
			}
		}
		return blocks
	}
	declares := func(sheet, selector, decl string) bool {
		for _, block := range declared(sheet, selector) {
			if regexp.MustCompile(decl).MatchString(block) {
				return true
			}
		}
		return false
	}

	forced, ordinary, found := atRuleBody(sheet, regexp.MustCompile(`@media \(forced-colors: active\) \{\s*\.goen-meter__bar`))
	if !found {
		t.Fatalf("%s: no forced-colours query draws the meter's bar", AppCSS)
	}
	for _, want := range []struct{ sheet, where, selector, decl string }{
		{ordinary, "", ".goen-meter__bar", `appearance:\s*none;`},
		{ordinary, "", ".goen-meter__bar", `--meter-fill:\s*var\(--cta\);`},
		{ordinary, "", ".goen-meter--met .goen-meter__bar", `--meter-fill:\s*var\(--success-text\);`},
		{ordinary, "", ".goen-meter__bar::-webkit-progress-value", `background:\s*var\(--meter-fill\);`},
		{ordinary, "", ".goen-meter__bar::-moz-progress-bar", `background:\s*var\(--meter-fill\);`},
		{forced, "in forced colours ", ".goen-meter__bar", `border:\s*1px solid CanvasText;`},
		{forced, "in forced colours ", ".goen-meter__bar::-webkit-progress-value", `background:\s*Highlight;`},
		{forced, "in forced colours ", ".goen-meter__bar::-moz-progress-bar", `background:\s*Highlight;`},
	} {
		if !declares(want.sheet, want.selector, want.decl) {
			t.Errorf("%s: %sno rule for %s declares %s", AppCSS, want.where, want.selector, want.decl)
		}
	}
	for _, rule := range cssRule.FindAllStringSubmatch(sheet, -1) {
		if strings.Contains(rule[1], "::-webkit-progress") && strings.Contains(rule[1], "::-moz-progress") {
			t.Errorf("%s: %s names both engines' fill, so both drop the rule", AppCSS, strings.Join(strings.Fields(rule[1]), " "))
		}
	}
}
