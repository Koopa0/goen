package assets

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestAppCSSCartFactsAsDrawn checks the figures under the cart's name as the
// design draws them: each fact is as wide as its own words, so a long one moves
// to the next row whole and the row has no gap sized for a short one; the
// figure is medium; and the count's 件 and the subtotal's NT$ are the figure's
// own size, weight and colour. The shared fact line sets a minimum width and
// shrinks its unit, and these rules take both back for the cart only.
func TestAppCSSCartFactsAsDrawn(t *testing.T) {
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
				if strings.Join(strings.Fields(s), " ") == selector {
					blocks = append(blocks, rule[2])
				}
			}
		}
		return blocks
	}
	for _, want := range []struct{ selector, decl string }{
		{".goen-cart__facts .ui-statline", `--stat-min:\s*0;`},
		{".goen-cart__facts .ui-statline dd", `font-weight:\s*var\(--fw-medium\);`},
		{".goen-cart__facts .ui-statline dd small", `font-size:\s*inherit;`},
		{".goen-cart__facts .ui-statline dd small", `font-weight:\s*inherit;`},
		{".goen-cart__facts .ui-statline dd small", `color:\s*inherit;`},
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
}
