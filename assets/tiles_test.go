package assets

import (
	"io/fs"
	"regexp"
	"testing"
)

// Two cards share a row only when each track is half the row less half the
// column gap. A formula that fixes the gap at one value leaves the related
// products on one column wherever the grid's gap is wider.
func TestTheRelatedProductsFollowTheGridsColumnGap(t *testing.T) {
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
