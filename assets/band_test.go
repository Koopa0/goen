package assets

import (
	"io/fs"
	"regexp"
	"testing"
)

// TestAppCSSBandWithoutPhoto checks that the home page's band without a
// photograph keeps the page's column: it must take the page's column and no
// white lift instead of the photograph's track.
func TestAppCSSBandWithoutPhoto(t *testing.T) {
	t.Parallel()

	sheet, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	for name, rule := range map[string]string{
		"a photo-less body spans the row":     `(?s)\.goen-band__body:first-child \{\s*grid-column: 1 / -1;\s*padding-left: var\(--page-edge\);`,
		"a photo-less band has no white lift": `(?s)\.goen-band:not\(:has\(\.goen-band__media\)\) \{\s*--lift: 0px;`,
	} {
		if !regexp.MustCompile(rule).Match(sheet) {
			t.Errorf("%s: %s lacks the rule", name, AppCSS)
		}
	}
}
