package assets

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestAppCSSRelatedProductsGap checks that the related products follow the grid's
// column gap. Two cards share a row only when each track is half the row less half
// the column gap. A formula that fixes the gap at one value leaves the related
// products on one column wherever the grid's gap is wider.
func TestAppCSSRelatedProductsGap(t *testing.T) {
	t.Parallel()

	sheet, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	for name, rule := range map[string]string{
		"the grid's column gap is a variable":        `(?s)\.goen-tiles__grid \{\s*--tiles-gap-x: 1rem;[^}]*gap: 2\.5rem var\(--tiles-gap-x\);`,
		"the grid widens the variable from 768":      `(?s)@media \(min-width: 768px\) \{\s*\.goen-tiles__grid \{\s*--tiles-gap-x: 1\.5rem;`,
		"the grid's two columns subtract half of it": `(?s)\.goen-tiles__grid \{[^}]*max\(6\.5rem, 50% - var\(--tiles-gap-x\) / 2\)`,
		"the related products subtract half of it":   `(?s)\.goen-pdp \.goen-tiles__grid \{\s*grid-template-columns: repeat\(auto-fill, minmax\(min\(100%, max\(6\.5rem, 50% - var\(--tiles-gap-x\) / 2\)\), 1fr\)\);`,
	} {
		if !regexp.MustCompile(rule).Match(sheet) {
			t.Errorf("%s: %s lacks the rule", name, AppCSS)
		}
	}
	if regexp.MustCompile(`50% - 0\.5rem`).Match(sheet) {
		t.Errorf("%s still halves a fixed 1rem gap", AppCSS)
	}
}

// TestAppCSSTileAsDrawn checks the four rules that make a product tile the
// catalogue's: the brand is read with the link and not shown, the photograph is
// square and uncornered on its well, a campaign price is in the colour of what
// runs out, and a sold-out colour's line is in the text's ink. Each reads a
// role, not a hex, so the night takes its own value from the same rule.
func TestAppCSSTileAsDrawn(t *testing.T) {
	t.Parallel()

	body, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	sheet := cssComment.ReplaceAllString(string(body), "")
	declared := func(selector string) []string {
		var blocks []string
		for _, rule := range cssRule.FindAllStringSubmatch(sheet, -1) {
			for _, s := range strings.Split(rule[1], ",") {
				if strings.TrimSpace(s) == selector {
					blocks = append(blocks, rule[2])
				}
			}
		}
		return blocks
	}
	for _, want := range []struct{ selector, decl string }{
		{".goen-tile__brand", `clip-path:\s*inset\(50%\)`},
		{".goen-tile__brand", `position:\s*absolute`},
		{".goen-tile__media", `aspect-ratio:\s*1;`},
		{".goen-tile__media", `border-radius:\s*0;`},
		{".goen-tile__media", `background:\s*var\(--photo\);`},
		{".goen-tile__price", `color:\s*var\(--price\);`},
		{".goen-tile__amount--sale", `color:\s*var\(--sys-tertiary\);`},
		{".goen-tile__was", `text-decoration:\s*line-through;`},
		{".goen-tile__slash", `stroke:\s*var\(--sys-on-surface\);`},
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
