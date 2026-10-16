package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
)

func TestADepartmentPageDoesNotLazyLoadItsFirstScreen(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ListingView{
		Slug: "tech", Name: "Tech", Products: tiledShelf(16), Total: 30, Page: 1, PageSize: 24,
		Theme: &Theme{Photo: components.Photo{URL: "/img/tech.webp", Alt: "tech"}},
	}
	page := renderToString(t, Listing(ListingMeta(ctx, view), view, nil, nil))

	assertEveryImageReservesItsBox(t, page)
	for _, tag := range imageTags(page) {
		if strings.Contains(tag, `class="goen-pagehead__photo"`) && strings.Contains(tag, `loading="lazy"`) {
			t.Errorf("the department photograph is lazy: %s", tag)
		}
	}
	tiles := tileImages(page)
	for i, tag := range tiles[:eagerTiles] {
		if strings.Contains(tag, `loading="lazy"`) {
			t.Errorf("tile %d of the first screen is lazy: %s", i, tag)
		}
	}
	if !strings.Contains(tiles[len(tiles)-1], `loading="lazy"`) {
		t.Error("the last tile of the shelf is not lazy: nothing is deferred")
	}
	if got := strings.Count(page, `fetchpriority="high"`); got != 1 {
		t.Errorf("%d high-priority photographs, want the first tile's alone", got)
	}
	if !strings.Contains(tiles[0], `fetchpriority="high"`) {
		t.Errorf("the first tile is not high priority: %s", tiles[0])
	}
}
