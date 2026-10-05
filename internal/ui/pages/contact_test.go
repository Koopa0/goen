package pages_test

import (
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestPublicContactUsesTheOwnedEmail(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for name, component := range map[string]templ.Component{
			"contact": pages.Contact(layouts.Page{}, pages.ContactForm{}),
			"footer":  layouts.Footer(layouts.NewsletterState{}),
		} {
			t.Run(string(locale)+"/"+name, func(t *testing.T) {
				var out strings.Builder
				if err := component.Render(i18n.WithLocale(t.Context(), locale), &out); err != nil {
					t.Fatal(err)
				}
				html := out.String()
				if !strings.Contains(html, `href="mailto:contact@koopa0.dev">contact@koopa0.dev</a>`) && name == "footer" {
					t.Error("footer does not expose the owned mailbox")
				}
				if !strings.Contains(html, `href="mailto:contact@koopa0.dev"`) || !strings.Contains(html, `>contact@koopa0.dev<`) {
					t.Error("contact destination or visible mailbox is missing")
				}
				for _, forbidden := range []string{"support@goen.tw", "02-2700-1234", "tel:+886227001234"} {
					if strings.Contains(html, forbidden) {
						t.Errorf("unowned contact detail %q remains", forbidden)
					}
				}
			})
		}
	}
}

// TestTheOrderNumberFieldShowsTheFormatGoenIssues: a placeholder in another
// shape, like "#GO-", is typed back and refused.
func TestTheOrderNumberFieldShowsTheFormatGoenIssues(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		var b strings.Builder
		if err := pages.Contact(layouts.Page{Title: "contact"}, pages.ContactForm{}).Render(i18n.WithLocale(t.Context(), locale), &b); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		if !strings.Contains(out, `placeholder="GO-260101-000001"`) {
			t.Errorf("%s: the order number field does not show an order number in the issued format", locale)
		}
		if strings.Contains(out, `placeholder="#GO-"`) {
			t.Errorf("%s: the order number field still shows #GO-", locale)
		}
	}
}
