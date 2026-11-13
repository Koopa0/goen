package assets

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestAppCSSCheckoutAsDrawn checks the checkout's column starts at the page's
// left edge, where the header's text and the page's name start, and is not
// centred in the page: the box is the page's own width and only its children
// are held to the 720px measure. A rule that narrows the box itself centres the
// column again, because the box's auto margins then centre what is left.
func TestAppCSSCheckoutAsDrawn(t *testing.T) {
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
	column := false
	for _, block := range declared(".goen-checkout > *") {
		if regexp.MustCompile(`max-width:\s*45rem;`).MatchString(block) {
			column = true
		}
	}
	if !column {
		t.Errorf("%s: no rule for .goen-checkout > * holds the column to 45rem", AppCSS)
	}
	maxWidth := regexp.MustCompile(`max-width:\s*([^;]+);`)
	for _, block := range declared(".goen-checkout") {
		for _, m := range maxWidth.FindAllStringSubmatch(block, -1) {
			if strings.TrimSpace(m[1]) != "var(--page-box)" {
				t.Errorf("%s: .goen-checkout is narrowed to %s, which centres the column in the page", AppCSS, m[1])
			}
		}
	}
}
