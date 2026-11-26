package assets

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestAppCSSComparePageAsDrawn checks the comparison as the flagship board
// draws it: the table in a framed box that scrolls on its own, each product on
// the photo plate with the studio ground multiplied away, the values in the
// data face with figures of one width, a differing row on the cobalt tint, and
// the differences-only switch hiding every other row but the one that buys. The
// header's count is the commerce cobalt, not the cart's ink. Each reads a role
// or a token, so the night takes its own value from the same rule.
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
		{".goen-compare__scroll", `border:\s*1px solid var\(--hairline\);`},
		{".goen-compare__scroll", `overflow-x:\s*auto;`},
		{".goen-compare__table td", `border-bottom:\s*1px solid var\(--hairline\);`},
		{".goen-compare__plate", `background:\s*var\(--photo-plate\);`},
		{".goen-compare__image", `mix-blend-mode:\s*multiply;`},
		{".goen-compare__table tbody td", `font-family:\s*var\(--font-data\);`},
		{".goen-compare__table tbody td", `font-variant-numeric:\s*tabular-nums`},
		{".goen-compare__table .is-differs td", `background:\s*var\(--cobalt-soft\);`},
		{".goen-compare__table .is-differs th", `background:\s*var\(--cobalt-soft\);`},
		{".goen-compare:has(#compare-diff:checked) .goen-compare__table tbody tr:not(.is-differs):not(.goen-compare__buy)", `display:\s*none;`},
		{".goen-compare__table tbody th", `position:\s*sticky;`},
		{".goen-header__comparecount", `background:\s*var\(--cta\);`},
		{".goen-header__comparecount", `color:\s*var\(--on-cta\);`},
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
	// The photograph is never a white sticker: nothing paints a ground of its
	// own under it inside the plate.
	for _, block := range declared(".goen-compare__image") {
		if regexp.MustCompile(`background`).MatchString(block) {
			t.Errorf("%s: .goen-compare__image paints a ground over the plate: %s", AppCSS, strings.TrimSpace(block))
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
