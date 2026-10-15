package alpha_test

import (
	"strings"
	"testing"

	"example.com/fix/alpha"
)

// TestParseEmpty checks that an empty line has no fields.
func TestParseEmpty(t *testing.T) {
	if got := alpha.Parse(strings.TrimSpace("  ")); len(got) != 0 {
		t.Errorf("Parse(blank) = %v, want none", got)
	}
}

// TestSame is alpha's own test; beta declares one of the same name.
func TestSame(t *testing.T) {
	if !(alpha.Store{}).Decide(1) {
		t.Error("Decide(1) = false, want true")
	}
}

// TestAlphaKeeps is not renamed, and a -run prefix selects it.
func TestAlphaKeeps(t *testing.T) {
	t.Run("case", func(t *testing.T) { _ = alpha.Parse("a b") })
}
