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

// TestAppCSSTileAsDrawn checks the rules that make a product tile the
// catalogue's: the brand is read with the link and not shown, the photograph is
// square and uncornered on its well, a campaign price is in the colour of what
// runs out, and a sold-out colour's line is in the text's ink; in English the
// name keeps room for two lines so a row's prices stand level. Each reads a
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
		{":lang(en) .goen-tile__name", `min-height:\s*3em;`},
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

// TestAppCSSCompareBarAsDrawn checks the compare box and bar as the design
// draws them: the box is a square of the control boundary on the page ground,
// ink when ticked, with the ring on focus and a 44px label; the bar is a tool
// strip on the high container, under the grid and never pinned to the window,
// and its one action is the ink button, which no rule of the bar turns blue.
// Each reads a role, so the night takes its value from the same rule.
func TestAppCSSCompareBarAsDrawn(t *testing.T) {
	t.Parallel()

	body, err := fs.ReadFile(files, AppCSS)
	if err != nil {
		t.Fatalf("read %s: %v", AppCSS, err)
	}
	sheet := cssComment.ReplaceAllString(string(body), "")
	rules := cssRule.FindAllStringSubmatch(sheet, -1)
	declared := func(selector string) []string {
		var blocks []string
		for _, rule := range rules {
			for _, s := range strings.Split(rule[1], ",") {
				if strings.Join(strings.Fields(s), " ") == selector {
					blocks = append(blocks, rule[2])
				}
			}
		}
		return blocks
	}
	box := ".goen-tile__compare input.goen-check"
	for _, want := range []struct{ selector, decl string }{
		{".goen-tile__compare", `min-height:\s*44px;`},
		{".goen-tile__compare", `font:\s*var\(--type-body-s\);`},
		{".goen-tile__compare:has(:checked)", `font-weight:\s*var\(--fw-medium\);`},
		{box, `appearance:\s*none;`},
		{box, `background:\s*var\(--sys-surface\);`},
		{box, `border:\s*1px solid var\(--sys-outline\);`},
		{box, `border-radius:\s*2px;`},
		{box, `height:\s*1\.125rem;`},
		{box, `width:\s*1\.125rem;`},
		{box + ":checked", `background:\s*var\(--sys-on-surface\);`},
		{box + ":checked::after", `border:\s*solid var\(--sys-surface\);`},
		{box + ":focus-visible", `outline:\s*2px solid var\(--ring\);`},
		{box + ":focus-visible", `outline-offset:\s*2px;`},
		{".goen-compare-pick__go", `background:\s*var\(--sys-surface-container-high\);`},
		{".goen-compare-pick__go", `padding:\s*var\(--s-3\) var\(--s-5\);`},
		{".goen-compare-pick__go", `margin-top:\s*var\(--s-6\);`},
		{".goen-compare-pick__go", `flex-wrap:\s*wrap;`},
		{".goen-compare-pick__note", `color:\s*var\(--sys-on-surface-variant\);`},
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
	pinned := regexp.MustCompile(`position:\s*(sticky|fixed)`)
	blue := regexp.MustCompile(`--sys-primary|--primary-faint|--primary-deep|--secondary`)
	for _, rule := range rules {
		if strings.Contains(rule[1], "goen-compare-pick") && pinned.MatchString(rule[2]) {
			t.Errorf("%s: %s pins the compare bar to the window: %s", AppCSS, strings.TrimSpace(rule[1]), pinned.FindString(rule[2]))
		}
		// The bar's one action is the ink button the drawing sets there, so no
		// rule of the bar lays blue on it.
		if strings.Contains(rule[1], "goen-compare-pick") && blue.MatchString(rule[2]) {
			t.Errorf("%s: %s draws the bar in blue: %s", AppCSS, strings.TrimSpace(rule[1]), blue.FindString(rule[2]))
		}
	}
}
