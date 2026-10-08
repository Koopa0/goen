package assets

import (
	"io/fs"
	"regexp"
	"testing"
)

// The carousel's track stretches every slide to the tallest one. A slide
// without a photograph under 1024px must put its name at the top and the rest
// at the foot, or its copy sits in the corner of a field of tone as tall as a
// photograph.
func TestAHeroSlideWithoutAPhotographIsAPosterBelowDesktop(t *testing.T) {
	t.Parallel()

	sheet, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	for name, rule := range map[string]string{
		"the body is a flex box":         `(?s)@media \(max-width: 63\.99rem\) \{\s*\.goen-hero__slide--text \.goen-hero__body \{\s*display: flex;`,
		"the copy fills the body":        `(?s)\.goen-hero__slide--text \.goen-hero__inner \{\s*flex: 1;\s*max-width: none;`,
		"the name leaves the rest below": `(?s)\.goen-hero__slide--text \.goen-hero__title \{\s*margin-block-end: auto;`,
	} {
		if !regexp.MustCompile(rule).Match(sheet) {
			t.Errorf("%s: %s lacks the rule", name, AppCSS)
		}
	}
}
