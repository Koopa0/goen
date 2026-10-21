package wishlist

import (
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
)

// renderToString is the one ui/pages keeps for its own tests, in
// ordernotfound_test.go. A test in another package cannot import it, so the
// moved wishlist page tests get their own copy, the same as account did.
func renderToString(t *testing.T, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(i18n.WithLocale(t.Context(), i18n.ZhHant), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}
