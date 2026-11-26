package assets

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestAppCSSComparePageAsDrawn checks the comparison as a printed table is
// drawn: a rule over it and one under each row, with no frame, no corner on the
// box or the photographs, the page's name and the section's name in the
// editorial face. Each reads a role or a type step, so
// the night takes its own value from the same rule.
func TestAppCSSComparePageAsDrawn(t *testing.T) {
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
		{".goen-compare__scroll", `border-top:\s*1px solid var\(--rule-line\);`},
		{".goen-compare__table td", `border-bottom:\s*1px solid var\(--rule-line\);`},
		{".goen-compare .goen-pagehead__title", `font:\s*var\(--fw-medium\) var\(--t-40\)/1\.2 var\(--font-display\);`},
		{".goen-compare__suggesthead", `font:\s*var\(--type-headline-m\);`},
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
	// No frame and no corner anywhere on the table's box or its photographs.
	cornered := regexp.MustCompile(`border-radius:|(?:^|[;\s])border:`)
	for _, selector := range []string{".goen-compare__scroll", ".goen-compare__table", ".goen-compare__image"} {
		for _, block := range declared(selector) {
			if cornered.MatchString(block) {
				t.Errorf("%s: %s is framed or cornered: %s", AppCSS, selector, strings.TrimSpace(block))
			}
		}
	}
	// No later rule gives a section's name a size, weight or family of its own.
	for _, selector := range []string{".goen-compare__suggesthead"} {
		for _, block := range declared(selector) {
			if regexp.MustCompile(`font-(size|weight|family):`).MatchString(block) {
				t.Errorf("%s: a rule for %s overrides the type step: %s", AppCSS, selector, strings.TrimSpace(block))
			}
		}
	}
}
