package home

import (
	"strings"
	"testing"
)

// TestSubCategoryLineNoBreak holds that subCategoryLine never starts a line
// with the dot: a no-break space comes before each dot.
func TestSubCategoryLineNoBreak(t *testing.T) {
	t.Parallel()

	got := subCategoryLine([]string{"Phones", "Laptops", "Accessories"})
	if strings.Contains(got, " · ") {
		t.Errorf("subCategoryLine = %q, want a no-break space before each dot", got)
	}
	if want := "Phones · Laptops · Accessories"; got != want {
		t.Errorf("subCategoryLine = %q, want %q", got, want)
	}
	if got := subCategoryLine(nil); got != "" {
		t.Errorf("subCategoryLine(nil) = %q, want empty", got)
	}
}
