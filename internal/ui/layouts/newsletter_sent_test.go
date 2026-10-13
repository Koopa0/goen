package layouts_test

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestInlineNewsletterConfirmationKeepsTheDestinationAndRefetchesItsForm(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			var out strings.Builder
			if err := layouts.NewsletterForm(layouts.NewsletterState{Done: true, Email: "n***@example.com"}).Render(i18n.WithLocale(t.Context(), locale), &out); err != nil {
				t.Fatal(err)
			}
			body := out.String()
			for _, want := range []string{"n***@example.com", `href="/#newsletter-form"`, `role="status"`, `id="newsletter-form"`, `hx-get="/"`, `hx-select="#newsletter-form"`, `hx-target="#newsletter-form"`} {
				if !strings.Contains(body, want) {
					t.Errorf("NewsletterForm() sent state omits %q", want)
				}
			}
			// Both the submitted form and its recovery link keep outerHTML: every
			// recovery must remove the sent state and restore the complete form.
			if strings.Count(body, `hx-swap="outerHTML"`) != 2 {
				t.Error("NewsletterForm() recovery does not replace the complete sent form")
			}
			if strings.Contains(body, `type="email"`) || strings.Contains(body, `type="submit"`) {
				t.Error("NewsletterForm() sent state still invites repeated submissions")
			}
			if locale == i18n.En && !strings.Contains(body, "If n***@example.com is not already subscribed") {
				t.Error("NewsletterForm() sent state claims a message was sent for every membership state")
			}
		})
	}
}
