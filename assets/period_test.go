package assets

import (
	"io/fs"
	"regexp"
	"testing"
)

// screenRule and forcedRule find a declaration block's start inside the
// period's two media blocks: the page's own colours, and forced colours.
func screenRule(selector, declarations string) string {
	return `(?s)@media \(forced-colors: none\) \{[^@]*?` + selector + ` \{\s*` + declarations
}

func forcedRule(selector, declarations string) string {
	return `(?s)@media \(forced-colors: active\) \{[^@]*?` + selector + ` \{\s*` + declarations
}

// dash is a gradient's first stop that leaves a gap, and tile the 8px repeat
// that turns the gap into even dashes: a stop of 100% or a cell-wide tile would
// draw the goodwill days as one solid line, like the statutory days.
const (
	dash = `(?:[1-9]|[1-9][0-9])%`
	tile = ` 0 0 / 8px 100% round border-box;`
)

func checkRules(t *testing.T, rules map[string]string) {
	t.Helper()
	sheet, err := fs.ReadFile(files, BaseCSS)
	if err != nil {
		t.Fatalf("read %s: %v", BaseCSS, err)
	}
	for name, rule := range rules {
		if !regexp.MustCompile(rule).Match(sheet) {
			t.Errorf("%s: %s lacks the rule", name, BaseCSS)
		}
	}
}

// The days goen adds after the statutory return window are its offer, not the
// customer's right, so they never draw like the statutory days. They are even
// dashes, a gradient under a transparent border, in forced colours too, where
// a dashed border paints solid on a short cell. An elapsed day takes its tile
// from the rule under it and changes only the colour.
func TestTheSpanAfterAMarkIsDrawnDashed(t *testing.T) {
	t.Parallel()
	checkRules(t, map[string]string{
		"dashes in the track's colour": screenRule(`\.ui-period > i\[data-span="extra"\]`,
			`background: linear-gradient\(90deg, var\(--period-track\) `+dash+`, transparent 0\)`+tile+`\s*border-bottom-color: transparent;`),
		"dashes in the fill once past": screenRule(`\.ui-period > i\[data-span="extra"\]\[data-cell\]`,
			`background-image: linear-gradient\(90deg, var\(--period-fill\) `+dash+`, transparent 0\);`),
		"forced: system colours kept": forcedRule(`\.ui-period > i\[data-span="extra"\],\s*\.ui-period > i\[data-cell="today"\]`,
			`border-bottom-color: transparent;\s*forced-color-adjust: none;`),
		"forced: dashes in GrayText": forcedRule(`\.ui-period > i\[data-span="extra"\]`,
			`background: linear-gradient\(90deg, GrayText `+dash+`, transparent 0\)`+tile),
		"forced: dashes in CanvasText once past": forcedRule(`\.ui-period > i\[data-span="extra"\]\[data-cell\]`,
			`background-image: linear-gradient\(90deg, CanvasText `+dash+`, transparent 0\);`),
	})
}

// Today's cell is half filled, so a period's last day does not read as over;
// among the goodwill dashes it is that half cut into the dashes.
func TestTodaysCellIsHalfFilled(t *testing.T) {
	t.Parallel()
	half := `background: linear-gradient\(90deg, var\(--period-fill\) 50%, var\(--period-track\) 0\) border-box;`
	forcedHalf := `background:\s*linear-gradient\(CanvasText, CanvasText\) 0 0 / 50% 100% no-repeat border-box,\s*linear-gradient\(GrayText, GrayText\) 100% 50% / 50% 2px no-repeat border-box;`
	dashes := `mask: linear-gradient\(90deg, #000 ` + dash + `, transparent 0\)` + tile
	checkRules(t, map[string]string{
		"today":                          screenRule(`\.ui-period > i\[data-cell="today"\]:not\(\[data-span\]\)`, half+`\s*border-bottom-color: transparent;`),
		"today among the dashes":         screenRule(`\.ui-period > i\[data-span="extra"\]\[data-cell="today"\]`, half+`\s*`+dashes),
		"forced: today":                  forcedRule(`\.ui-period > i\[data-span="extra"\]\[data-cell="today"\],\s*\.ui-period > i\[data-cell="today"\]:not\(\[data-span\]\)`, forcedHalf),
		"forced: today among the dashes": forcedRule(`\.ui-period > i\[data-span="extra"\]\[data-cell="today"\]`, dashes),
	})
}
