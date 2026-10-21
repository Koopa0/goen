package contact_test

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/contact"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/fieldrule"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// What a field says in markup is what the rule says: the pattern, the script's
// hook and the server's own message, and nothing for a field with no rule.
func TestARuleWritesItselfIntoTheMarkup(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, contact.Contact(layouts.Page{Title: "contact"}, contact.Form{}))
	rule := fieldrule.Email
	for _, want := range []string{
		`data-rule="email"`,
		`data-rule-message="` + i18n.T(ctx, i18n.KeyEmailMalformed) + `"`,
		`data-rule-error="contact-email-error"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the contact email field is missing %q", want)
		}
	}
	if !strings.Contains(html, `pattern="`) || rule.Pattern == "" {
		t.Error("the contact email field carries no pattern")
	}
}
