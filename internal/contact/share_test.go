package contact_test

import (
	"context"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/contact"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

func renderHeadAt(t *testing.T, ctx context.Context, p frame.Page) string {
	t.Helper()
	return renderComponent(t, frame.WithSiteOrigin(ctx, "https://goen.test"), frame.Base(p))
}

// The department and product rows are catalog's TestCatalogPagesSendTheirOwnLinkPreviewTags,
// the campaign's row campaign's TestMetaLinkPreview,
// the home row home's TestTheHomePageSendsItsOwnLinkPreviewTags.
func TestEveryPageKindSendsItsOwnLinkPreviewTags(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(frame.WithRequestPath(t.Context(), "/c/books"), i18n.ZhHant)
	for name, tt := range map[string]struct {
		page frame.Page
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
