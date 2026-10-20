package banners

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/uitest"
)

// From TestDeskRefusalsNameTheirFormAndLinkToTheFirstInvalidControl, which keeps
// the product half.
func TestARefusedBannerNamesItsFormAndLinksToTheFirstInvalidControl(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			bad := i18n.T(ctx, i18n.KeyAdminNoticeRefused)
			body := renderComponent(t, ctx, Banners(layouts.Page{}, &BannersView{Errors: map[string]string{"message_en": bad, "cta": bad}}))
			uitest.AssertRefusalSummary(t, body, uitest.RefusedForm{
				Action: "/admin/banners#new-banner", Target: "new-banner", First: "b-message-en",
				Title: i18n.T(ctx, i18n.KeyAdminRefusalBanner), Count: 2,
			})
		})
	}
}
