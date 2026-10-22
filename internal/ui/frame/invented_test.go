package frame_test

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/contact"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

func TestNoPageNamesAnInventedCompanyOrAddress(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		for name, html := range map[string]string{
			"contact": renderComponent(t, ctx, contact.Contact(contact.Meta(ctx), contact.Form{})),
			"footer":  renderComponent(t, ctx, frame.Footer(frame.NewsletterState{})),
		} {
			for _, forbidden := range []string{"90123456", "Co., Ltd.", "松高路", "google.com/maps"} {
				if strings.Contains(html, forbidden) {
					t.Errorf("%s/%s still carries %q", locale, name, forbidden)
				}
			}
			if name == "footer" && !strings.Contains(html, "© 2026 goen<") {
				t.Errorf("%s footer copyright is not the bare name", locale)
			}
		}
	}
}
