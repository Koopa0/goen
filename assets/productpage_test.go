package assets

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestAppCSSProductPageAsDrawn checks what the drawing fixes on the product
// page: the name and a section's head are set in the display face, each by one
// rule; the photographs stand on the plate, multiplied into it, and the chosen
// thumbnail wears the cobalt edge; the price is the shop's price colour in the
// face of figures, and what a campaign saves is a seal filled in the savings
// red, while the buy bar's sale price is the colour of what ends; a word
// option is a square box chosen by an ink fill and a colour a round chip
// chosen by a 2px ink ring, neither in blue, neither with a mark drawn as
// content, both with their names shown; the add button is the cobalt
// commerce action; the stock line stands on the shelf; the restock notice is
// on the savings tint; the time left in a campaign's line is vermilion, as
// what ends is everywhere, and the few left of a stock is ink; a link to
// another page is ink with its faint underline (the guest's wishlist, the
// policies) and the one next step of a section is blue. Each reads a role or a
// step, not a value, so the night and the narrow screen take their own from
// the same rule.
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
		{".goen-pdp__title", `font:\s*var\(--fw-semibold\) clamp\([^;]+\)/1\.15 var\(--font-display\);`},
		{".goen-pdp__sectiontitle", `font:\s*var\(--fw-semibold\) clamp\([^;]+\)/1\.25 var\(--font-display\);`},
		{".goen-pdp__gallery::before", `background:\s*var\(--photo-plate\);`},
		{".goen-pdp__shotimg", `mix-blend-mode:\s*multiply;`},
		{".goen-pdp__thumbimg", `mix-blend-mode:\s*multiply;`},
		{".goen-pdp__pick:checked ~ .goen-pdp__thumb", `border-color:\s*var\(--cta\);`},
		{".goen-pdp__price", `font-family:\s*var\(--font-data\);`},
		{".goen-pdp__seal", `background:\s*var\(--savings\);`},
		{".goen-pdp__seal", `color:\s*var\(--on-savings\);`},
		{".goen-buybar__price--sale", `color:\s*var\(--sys-tertiary\);`},
		{".goen-swatch", `border-radius:\s*var\(--shape-xs\);`},
		{".goen-swatch--on", `background:\s*var\(--fill-ink\);`},
		{".goen-swatch--on", `color:\s*var\(--on-fill-ink\);`},
		{".goen-swatch--dot", `flex-direction:\s*row;`},
		{".goen-swatch--dot", `border-radius:\s*var\(--shape-full\);`},
		{".goen-swatch--dot.goen-swatch--on", `border-color:\s*var\(--sys-on-surface\);`},
		{".goen-swatch--dot.goen-swatch--on", `box-shadow:\s*inset 0 0 0 1px var\(--sys-on-surface\);`},
		{".goen-swatch--on.goen-swatch--out", `border-color:\s*var\(--sys-on-surface\);`},
		{".goen-pdp__add:where(.goen-btn--primary)", `background:\s*var\(--cta\);`},
		{".goen-pdp__add:where(.goen-btn--primary)", `color:\s*var\(--on-cta\);`},
		{".goen-pdp__stock", `background:\s*var\(--shelf\);`},
		{".goen-pdp__notify", `background:\s*var\(--savings-soft\);`},
		{".goen-pdp__source .goen-ends__left", `color:\s*var\(--sys-tertiary\);`},
		{".goen-pdp__lowstock", `color:\s*var\(--on-surface-strong\);`},
		{".goen-pdp__wishlink", `color:\s*var\(--sys-on-surface\);`},
		{".goen-pdp__wishlink", `text-decoration-line:\s*underline;`},
		{".goen-pdp__policies a", `color:\s*var\(--sys-on-surface\);`},
		{".goen-pdp__compare", `color:\s*var\(--sys-primary\);`},
		{".goen-pdp__browse", `color:\s*var\(--sys-primary\);`},
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
	// One rule sets the name's and a section's type: none after it gives either
	// a size, a family or a weight of its own, or puts it in the body face.
	for _, selector := range []string{".goen-pdp__title", ".goen-pdp__sectiontitle"} {
		for _, block := range declared(selector) {
			if regexp.MustCompile(`font-(size|family|weight):`).MatchString(block) {
				t.Errorf("%s: a rule for %s overrides its one font: %s", AppCSS, selector, strings.TrimSpace(block))
			}
		}
	}
	for _, rule := range cssRule.FindAllStringSubmatch(sheet, -1) {
		if regexp.MustCompile(`\.goen-pdp__(title|sectiontitle)\b`).MatchString(rule[1]) && regexp.MustCompile(`--font-sans`).MatchString(rule[2]) {
			t.Errorf("%s: %s sets the name or a section's head in the body face", AppCSS, strings.TrimSpace(rule[1]))
		}
	}
	// An option is not chosen in blue, which is the next step's and the focus
	// ring's, carries no mark drawn as content and never hides its name.
	for _, rule := range cssRule.FindAllStringSubmatch(sheet, -1) {
		if !strings.Contains(rule[1], ".goen-swatch") {
			continue
		}
		if bad := regexp.MustCompile(`--sys-primary|--primary-faint|--primary-deep|--cta|--cobalt|(?:^|[\s;{])content:|clip-path:`).FindString(rule[2]); bad != "" {
			t.Errorf("%s: %s declares %q: an option is chosen by ink, its name shown", AppCSS, strings.TrimSpace(rule[1]), bad)
		}
	}
	// Few left is not a thing that ends: no rule gives it the vermilion of a
	// deadline or a warning's amber.
	for _, block := range declared(".goen-pdp__lowstock") {
		if regexp.MustCompile(`--sys-tertiary|--warn-ink|--accent|--savings`).MatchString(block) {
			t.Errorf("%s: .goen-pdp__lowstock is drawn as a deadline or a warning: %s", AppCSS, strings.TrimSpace(block))
		}
	}
	// The price in the buy box is not painted as a sale: the seal beside it is
	// the one red, and a red price next to it would say it twice.
	for _, block := range declared(".goen-pdp__price") {
		if regexp.MustCompile(`--sys-tertiary|--savings`).MatchString(block) {
			t.Errorf("%s: the buy box's price is painted as a sale: %s", AppCSS, strings.TrimSpace(block))
		}
	}
}
