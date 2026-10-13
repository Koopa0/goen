package assets

import (
	"io/fs"
	"regexp"
	"testing"
)

// A later copy silently wins and makes edits to the first copy ineffective.
func TestSharedLayoutRulesAreDeclaredOnce(t *testing.T) {
	t.Parallel()
	for _, rule := range []struct{ sheet, selector string }{
		{AppCSS, ".goen-order__cancel"},
		{AdminCSS, ".goen-admin__side"},
		{AppCSS, "#filters-applied:empty"},
	} {
		sheet, err := fs.ReadFile(files, rule.sheet)
		if err != nil {
			t.Fatalf("read %s: %v", rule.sheet, err)
		}
		pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(rule.selector) + `\s*\{`)
		if got := len(pattern.FindAll(sheet, -1)); got != 1 {
			t.Errorf("%s declares %s %d times, want one top-level rule", rule.sheet, rule.selector, got)
		}
	}
}
