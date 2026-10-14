package pages

import (
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/fieldrule"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestSignInKeepsTheEmailRuleWithoutAttributingGoogleFailureToIt(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	body := renderComponent(t, ctx, SignIn(SignInMeta(ctx), AuthView{Errors: map[string]string{"oauth": "Google sign-in failed"}}))
	input := regexp.MustCompile(`<input[^>]*id="email"[^>]*>`).FindString(body)
	for _, want := range []string{`type="email"`, `data-rule="email"`, `autocomplete="email"`, `required`} {
		if !strings.Contains(input, want) {
			t.Errorf("sign-in email field omits %q: %s", want, input)
		}
	}
	if strings.Contains(input, `aria-invalid="true"`) || strings.Contains(input, `aria-describedby="signin-google-error"`) {
		t.Errorf("Google failure was attributed to the email field: %s", input)
	}
}

// What a field says in markup is what the rule says: the pattern, the script's
// hook and the server's own message, and nothing for a field with no rule.
func TestARuleWritesItselfIntoTheMarkup(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Contact(layouts.Page{Title: "contact"}, ContactForm{}))
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
