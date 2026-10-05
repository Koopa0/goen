package pages

import (
	"context"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func renderHeadAt(t *testing.T, ctx context.Context, p layouts.Page) string {
	t.Helper()
	return renderComponent(t, layouts.WithSiteOrigin(ctx, "https://goen.test"), layouts.Base(p))
}

func TestEveryPageKindSendsItsOwnLinkPreviewTags(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(layouts.WithRequestPath(t.Context(), "/c/books"), i18n.ZhHant)
	department := ListingView{
		Slug: "books", Name: "書籍",
		Theme: &Theme{Photo: Photo{URL: "/static/media/products/books.webp?v=1", Alt: "書架"}},
	}
	for name, tt := range map[string]struct {
		page layouts.Page
		want []string
	}{
		"home": {
			HomeMeta(ctx),
			[]string{`og:title" content="` + i18n.T(ctx, i18n.KeySiteTitle) + `"`},
		},
		"department": {
			ListingMeta(ctx, department),
			[]string{
				`og:title" content="書籍 · goen"`,
				`og:image" content="https://goen.test/static/media/products/books.webp?v=1"`,
				`og:image:alt" content="書架"`,
			},
		},
		"campaign": {
			CampaignMeta(ctx, "秋日選物", Photo{URL: "/static/media/campaigns/autumn.webp?v=1", Alt: "秋日"}),
			[]string{
				`og:title" content="秋日選物 · goen"`,
				`og:description" content="秋日選物 — goen 限時優惠"`,
				`og:image" content="https://goen.test/static/media/campaigns/autumn.webp?v=1"`,
			},
		},
		"about": {
			AboutMeta(ctx),
			[]string{`og:title" content="` + i18n.T(ctx, i18n.KeyAboutTitle) + ` · goen"`},
		},
		"contact": {
			ContactMeta(ctx),
			[]string{
				`og:title" content="` + i18n.T(ctx, i18n.KeyContactTitle) + ` · goen"`,
				`og:description" content="` + i18n.T(ctx, i18n.KeyContactDescription) + `"`,
			},
		},
		"product": {
			ProductMeta(&ProductView{Name: "Phone", Brand: "goen", Summary: "A phone", Images: []ProductImage{{URL: "/p.webp"}}}),
			[]string{`og:description" content="A phone"`, `og:image" content="https://goen.test/p.webp"`},
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

func TestADepartmentCarriesNoFillerDescription(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	head := renderHeadAt(t, ctx, ListingMeta(ctx, ListingView{Slug: "books", Name: "書籍"}))
	for _, forbidden := range []string{`name="description"`, `og:description`} {
		if strings.Contains(head, forbidden) {
			t.Errorf("a department with no subtitle sent %s", forbidden)
		}
	}
}

func TestNoPageNamesAnInventedCompanyOrAddress(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		for name, html := range map[string]string{
			"about":   renderComponent(t, ctx, About(AboutMeta(ctx))),
			"contact": renderComponent(t, ctx, Contact(ContactMeta(ctx), ContactForm{})),
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
