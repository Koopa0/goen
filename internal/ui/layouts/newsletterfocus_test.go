package layouts_test

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// The footer form swaps itself on a refusal; htmx restores focus only to an
// element with an id.
func TestTheNewsletterSubmitKeepsAnIDAcrossASwap(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := layouts.NewsletterForm(layouts.NewsletterState{}).Render(i18n.WithLocale(t.Context(), i18n.ZhHant), &b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `id="newsletter-submit"`) {
		t.Error("the newsletter submit button has no id")
	}
}
