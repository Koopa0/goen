package pages

import (
	"context"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/contact"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func renderHeadAt(t *testing.T, ctx context.Context, p layouts.Page) string {
	t.Helper()
	return renderComponent(t, layouts.WithSiteOrigin(ctx, "https://goen.test"), layouts.Base(p))
}

// The department and product rows are catalog's TestCatalogPagesSendTheirOwnLinkPreviewTags,
// the campaign's row campaign's TestTheCampaignPageSendsItsOwnLinkPreviewTags,
// the home row home's TestTheHomePageSendsItsOwnLinkPreviewTags.
func TestEveryPageKindSendsItsOwnLinkPreviewTags(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(layouts.WithRequestPath(t.Context(), "/c/books"), i18n.ZhHant)
	for name, tt := range map[string]struct {
		page layouts.Page
		want []string
	}{
		"contact": {
			contact.Meta(ctx),
			[]string{
				`og:title" content="` + i18n.T(ctx, i18n.KeyContactTitle) + ` · goen"`,
				`og:description" content="` + i18n.T(ctx, i18n.KeyContactDescription) + `"`,
			},
		},
	} {
		head := renderHeadAt(t, ctx, tt.page)
		for _, want := range append(tt.want,
			`og:type" content="website"`,
			`og:site_name" content="goen"`,
			`og:url" content="https://goen.test/c/books"`,
			`name="twitter:card" content="summary_large_image"`,
		) {
			if !strings.Contains(head, want) {
				t.Errorf("%s head omits %s", name, want)
			}
		}
	}
}

func TestNoPageNamesAnInventedCompanyOrAddress(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		for name, html := range map[string]string{
			"contact": renderComponent(t, ctx, contact.Contact(contact.Meta(ctx), contact.Form{})),
			"footer":  renderComponent(t, ctx, layouts.Footer(layouts.NewsletterState{})),
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
