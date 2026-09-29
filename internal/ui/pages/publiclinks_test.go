package pages_test

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestPublicContactOmitsUnownedSocialDestinations(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			var out strings.Builder
			if err := pages.Contact(layouts.Page{Description: i18n.T(i18n.WithLocale(t.Context(), locale), i18n.KeyContactDescription)}, pages.ContactForm{}).Render(i18n.WithLocale(t.Context(), locale), &out); err != nil {
				t.Fatal(err)
			}
			body := out.String()
			for _, unowned := range []string{"line.me/", "instagram.com/", "youtube.com/", "facebook.com/", "LINE"} {
				if strings.Contains(body, unowned) {
					t.Errorf("public contact page still advertises an unowned channel %q", unowned)
				}
			}
			for _, available := range []string{`action="/contact"`, `mailto:`, `class="goen-footer"`} {
				if !strings.Contains(body, available) {
					t.Errorf("contact page lost %q", available)
				}
			}
		})
	}
}
