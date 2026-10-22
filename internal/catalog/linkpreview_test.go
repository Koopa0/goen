package catalog

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestListingMetaAndProductMeta holds that the department and product pages
// send their own link preview tags.
//
// From TestMetaLinkPreviewTags, which keeps the other pages' rows;
// the campaign's row is internal/discount/campaign's TestMetaLinkPreview.
func TestListingMetaAndProductMeta(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(frame.WithRequestPath(t.Context(), "/c/books"), i18n.ZhHant)
	department := ListingView{
		Slug: "books", Name: "書籍",
		Theme: &Theme{Photo: widget.Photo{URL: "/static/media/products/books.webp?v=1", Alt: "書架"}},
	}
	for name, tt := range map[string]struct {
		page frame.Page
		want []string
	}{
		"department": {
			ListingMeta(ctx, department),
			[]string{
				`og:title" content="書籍 · goen"`,
				`og:image" content="https://goen.test/static/media/products/books.webp?v=1"`,
				`og:image:alt" content="書架"`,
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

// TestListingMetaNoFiller holds that a department with no subtitle carries no
// filler description.
func TestListingMetaNoFiller(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	head := renderHeadAt(t, ctx, ListingMeta(ctx, ListingView{Slug: "books", Name: "書籍"}))
	for _, forbidden := range []string{`name="description"`, `og:description`} {
		if strings.Contains(head, forbidden) {
			t.Errorf("a department with no subtitle sent %s", forbidden)
		}
	}
}
