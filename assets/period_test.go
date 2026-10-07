package assets

import (
	"io/fs"
	"regexp"
	"testing"
)

// The days goen adds after the statutory return window are its offer, not the
// customer's right, so they never draw like the statutory days. They are
// dashed: a gradient under a transparent border, and in forced colours, which
// drop a gradient, a dashed border.
func TestTheSpanAfterAMarkIsDrawnDashed(t *testing.T) {
	t.Parallel()

	sheet, err := fs.ReadFile(files, BaseCSS)
	if err != nil {
		t.Fatalf("read %s: %v", BaseCSS, err)
	}
	for name, rule := range map[string]string{
		"dashes in the track's colour": `(?s)@media \(forced-colors: none\) \{[^@]*?\.ui-period > i\[data-span="extra"\] \{\s*background: linear-gradient\(90deg, var\(--period-track\) \d+%, transparent 0\)[^;]*;\s*border-bottom-color: transparent;`,
		"dashes in the fill once past": `(?s)@media \(forced-colors: none\) \{[^@]*?\.ui-period > i\[data-span="extra"\]\[data-cell\] \{\s*background-image: linear-gradient\(90deg, var\(--period-fill\) \d+%, transparent 0\);`,
		"a dashed border when forced":  `(?s)@media \(forced-colors: active\) \{[^@]*?\.ui-period > i\[data-span="extra"\] \{\s*border-bottom-style: dashed;`,
	} {
		if !regexp.MustCompile(rule).Match(sheet) {
			t.Errorf("%s: %s lacks the rule", name, BaseCSS)
		}
	}
}
