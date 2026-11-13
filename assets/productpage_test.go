package assets

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestAppCSSProductPageAsDrawn checks what the drawing fixes on the product
// page: the name is set by the 40px step, never a size of its own, and a price
// with a struck original, a campaign's, is in the colour of what ends, in the
// buy box and in the mobile buy bar alike; an option is a square box of the
// control corner that is chosen by a 2px ink frame, and a colour says its name
// on its box; a section is named in the editorial face; the restock field and
// its button are stacked; the time left in a campaign's line is vermilion, as
// what ends is everywhere, and the few left of a stock is ink. Each reads a
// role or a step, not a value, so the night and the narrow screen take their
// own from the same rule.
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
		{".goen-swatch", `border-radius:\s*var\(--shape-xs\);`},
		{".goen-swatch--on", `border-color:\s*var\(--sys-on-surface\);`},
		{".goen-swatch--on", `box-shadow:\s*inset 0 0 0 1px var\(--sys-on-surface\);`},
		{".goen-swatch--dot", `flex-direction:\s*row;`},
		{".goen-pdp__sectiontitle", `font:\s*var\(--type-headline-m\);`},
		{".goen-pdp__notifyrow", `display:\s*grid;`},
		{".goen-pdp__source .goen-ends__left", `color:\s*var\(--sys-tertiary\);`},
		{".goen-pdp__lowstock", `color:\s*var\(--on-surface-strong\);`},
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
	// No later rule gives the name a size or a family of its own, and none puts
	// a section's title back in the body face or at a size of its own.
	for selector, step := range map[string]string{".goen-pdp__title": "40px step", ".goen-pdp__sectiontitle": "headline step"} {
		for _, block := range declared(selector) {
			if regexp.MustCompile(`font-(size|family|weight):`).MatchString(block) {
				t.Errorf("%s: a rule for %s overrides the %s: %s", AppCSS, selector, step, strings.TrimSpace(block))
			}
		}
	}
	for _, rule := range cssRule.FindAllStringSubmatch(sheet, -1) {
		if strings.Contains(rule[1], ".goen-pdp__sectiontitle") && regexp.MustCompile(`font-family:\s*var\(--font-sans\)`).MatchString(rule[2]) {
			t.Errorf("%s: %s sets a section's title in the body face", AppCSS, strings.TrimSpace(rule[1]))
		}
	}
	// An option is no pill, is not chosen in blue, carries no check mark drawn
	// as content and never hides its name: the frame is the state and the name
	// is on the box.
	for _, rule := range cssRule.FindAllStringSubmatch(sheet, -1) {
		if !strings.Contains(rule[1], ".goen-swatch") {
			continue
		}
		if bad := regexp.MustCompile(`--sys-primary|--primary-faint|--primary-deep|--r-press|--r-full|(?:^|[\s;{])content:|clip-path:`).FindString(rule[2]); bad != "" {
			t.Errorf("%s: %s declares %q: an option is a square box chosen by its ink frame, its name shown", AppCSS, strings.TrimSpace(rule[1]), bad)
		}
	}
	// Few left is not a thing that ends: no rule gives it the vermilion of a
	// deadline or a warning's amber.
	for _, block := range declared(".goen-pdp__lowstock") {
		if regexp.MustCompile(`--sys-tertiary|--warn-ink|--accent`).MatchString(block) {
			t.Errorf("%s: .goen-pdp__lowstock is drawn as a deadline or a warning: %s", AppCSS, strings.TrimSpace(block))
		}
	}
}
