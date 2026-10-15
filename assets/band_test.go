package assets

import (
	"io/fs"
	"regexp"
	"testing"
)

// The department head is the band, which was drawn for the home page: it must
// not bring the home's section margin, and without a photograph it must take
// the page's column and no white lift instead of the photograph's track.
func TestTheBandWithoutAPhotographKeepsThePageColumn(t *testing.T) {
	t.Parallel()

	sheet, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	for name, rule := range map[string]string{
		"the head carries no section margin":         `(?s)\.goen-pagehead \.goen-band \{\s*margin-top: 0;`,
		"a photo-less body spans the row":            `(?s)\.goen-band__body:first-child \{\s*grid-column: 1 / -1;\s*padding-left: var\(--page-edge\);`,
		"a photo-less band has no white lift":        `(?s)\.goen-band:not\(:has\(\.goen-band__media\)\) \{\s*--lift: 0px;`,
		"the head's photograph takes seven parts":    `(?s)\.goen-pagehead \.goen-band__grid \{\s*align-items: stretch;\s*grid-template-columns: minmax\(0, 7fr\) minmax\(0, 5fr\);`,
		"the head's title breaks inside a long word": `(?s)\.goen-listing__head \.goen-pagehead__title \{[^}]*overflow-wrap: anywhere;`,
		"a phone's photograph has no tone beside it": `(?s)@media \(max-width: 47\.99rem\) \{\s*\.goen-pagehead \.goen-band \{\s*background: none;`,
	} {
		if !regexp.MustCompile(rule).Match(sheet) {
			t.Errorf("%s: %s lacks the rule", name, AppCSS)
		}
	}
}
