package db_test

import (
	"testing"

	"github.com/koopa0/goen/internal/db"
)

// TestEscapeLikeLeavesNoWildcard: every search pattern goen builds from typed
// words, storefront and back office, escapes LIKE's syntax through this.
func TestEscapeLikeLeavesNoWildcard(t *testing.T) {
	t.Parallel()
	for q, want := range map[string]string{
		"%%":           `\%\%`,
		"a_b@goen.dev": `a\_b@goen.dev`,
		`50\%`:         `50\\\%`,
		"pixel":        "pixel",
		"":             "",
		`\`:            `\\`,
	} {
		if got := db.EscapeLike(q); got != want {
			t.Errorf("EscapeLike(%q) = %q, want %q", q, got, want)
		}
	}
}
