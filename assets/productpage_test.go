package assets

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestAppCSSProductPageAsDrawn checks the product page's two type rules the
// drawing fixes: the name is set by the 40px step, never a size of its own,
// and a price with a struck original, a campaign's, is in the colour of what
// ends, in the buy box and in the mobile buy bar alike. Each reads a role or a
// step, not a value, so the night and the narrow screen take their own from
// the same rule.
func TestAppCSSProductPageAsDrawn(t *testing.T) {
	t.Parallel()

	body, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	sheet := cssComment.ReplaceAllString(string(body), "")
	declared := func(selector string) []string {
		var blocks []string
		for _, rule := range cssRule.FindAllStringSubmatch(sheet, -1) {
			for s := range strings.SplitSeq(rule[1], ",") {
				if strings.TrimSpace(s) == selector {
					blocks = append(blocks, rule[2])
				}
			}
		}
		return blocks
	}
	for _, want := range []struct{ selector, decl string }{
		{".goen-pdp__title", `font:\s*var\(--fw-medium\) var\(--t-40\)/1\.2 var\(--font-editorial\);`},
		{".goen-pdp__price--sale", `color:\s*var\(--sys-tertiary\);`},
		{".goen-buybar__price--sale", `color:\s*var\(--sys-tertiary\);`},
	} {
		found := false
		for _, block := range declared(want.selector) {
			if regexp.MustCompile(want.decl).MatchString(block) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no rule for %s declares %s", AppCSS, want.selector, want.decl)
		}
	}
	// No later rule gives the name a size or a family of its own.
	for _, block := range declared(".goen-pdp__title") {
		if regexp.MustCompile(`font-(size|family):`).MatchString(block) {
			t.Errorf("%s: a rule for .goen-pdp__title overrides the 40px step: %s", AppCSS, strings.TrimSpace(block))
		}
	}
}
