package auth

import (
	"html"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestSignedInHandoff: the page after a Google sign-in loads the target it is
// given, in the refresh and in a link, whatever the target holds: a query that
// would close the attribute stays text, and the page says so in both locales.
func TestSignedInHandoff(t *testing.T) {
	t.Parallel()
	const target = `/cart?next=/p/headphones&qty=adjusted&x="><b>#items`
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		body := renderComponent(t, ctx, SignedInHandoff(target))
		for _, want := range []string{
			`<meta http-equiv="refresh" content="0; url=` + html.EscapeString(target) + `">`,
			`href="` + html.EscapeString(target) + `"`,
			i18n.T(ctx, i18n.KeyGoogleSignedIn),
			i18n.T(ctx, i18n.KeyGoogleContinue),
		} {
			if !strings.Contains(body, want) {
				t.Errorf("SignedInHandoff(%q) in %s lacks %s:\n%s", target, locale, want, body)
			}
		}
		if strings.Contains(body, "<b>") {
			t.Errorf("SignedInHandoff(%q) in %s let the target into the markup:\n%s", target, locale, body)
		}
		if n := strings.Count(body, "<html"); n != 1 {
			t.Errorf("SignedInHandoff(%q) in %s has %d <html elements, want 1", target, locale, n)
		}
	}
}
