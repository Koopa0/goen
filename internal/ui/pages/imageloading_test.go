package pages

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

var imgTag = regexp.MustCompile(`<img\b[^>]*>`)

func imageTags(html string) []string { return imgTag.FindAllString(html, -1) }

// assertEveryImageReservesItsBox fails for a photograph the browser would have
// to lay out again when it arrives.
func assertEveryImageReservesItsBox(t *testing.T, page string) {
	t.Helper()
	tags := imageTags(page)
	if len(tags) == 0 {
		t.Fatal("the page draws no <img>")
	}
	for _, tag := range tags {
		if !strings.Contains(tag, " width=") || !strings.Contains(tag, " height=") {
			t.Errorf("an <img> without width and height: %s", tag)
		}
	}
}

func tiledShelf(n int) []ProductTile {
	tiles := make([]ProductTile, 0, n)
	for i := range n {
		slug := fmt.Sprintf("p%d", i)
		tiles = append(tiles, ProductTile{
			Slug: slug, Name: slug, PriceCents: 1000, InStock: true,
			ImageURL: "/img/" + slug + ".webp", ImageAlt: slug, ImageWidth: 1600, ImageHeight: 1200,
		})
	}
	return tiles
}

func tileImages(page string) []string {
	var out []string
	for _, tag := range imageTags(page) {
		if strings.Contains(tag, `class="goen-tile__img"`) {
			out = append(out, tag)
		}
	}
	return out
}

func TestTheHomePageLoadsItsFirstScreenPhotographsAtOnceAndReservesEveryBox(t *testing.T) {
	t.Parallel()
	view := HomeView{
		Slides: []HeroSlide{
			{Layout: SlidePhoto, Photo: Photo{URL: "/img/hero.webp", Alt: "hero"}, PhotoWidth: 1600, PhotoHeight: 600, Title: "A"},
			{Layout: SlidePhoto, Photo: Photo{URL: "/img/hero2.webp", Alt: "hero"}, PhotoWidth: 1600, PhotoHeight: 600, Title: "B"},
		},
		Categories: []HomeCategory{
			{Slug: "a", Name: "A", Photo: Photo{URL: "/img/a.webp"}},
			{Slug: "b", Name: "B", Photo: Photo{URL: "/img/b.webp"}},
		},
		Row: ProductRow{Title: "Row", Href: "/s/row", Tiles: tiledShelf(10)},
	}
	page := renderIn(t, i18n.ZhHant, Home(layouts.Page{}, view))

	assertEveryImageReservesItsBox(t, page)
	for _, tag := range imageTags(page) {
		if strings.Contains(tag, `class="goen-cat__photo"`) && !strings.Contains(tag, `loading="lazy"`) {
			t.Errorf("a department photograph, below the product row, is not lazy: %s", tag)
		}
	}
	tiles := tileImages(page)
	if len(tiles) != 10 {
		t.Fatalf("%d tile photographs, want 10", len(tiles))
	}
	for i, tag := range tiles {
		wantLazy := i >= eagerTiles
		if got := strings.Contains(tag, `loading="lazy"`); got != wantLazy {
			t.Errorf("tile %d lazy = %t, want %t: %s", i, got, wantLazy, tag)
		}
	}
	if got := strings.Count(page, `fetchpriority="high"`); got != 1 {
		t.Errorf("%d high-priority photographs, want the hero's alone", got)
	}
}

func TestADepartmentPageDoesNotLazyLoadItsFirstScreen(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ListingView{
		Slug: "tech", Name: "Tech", Products: tiledShelf(16), Total: 30, Page: 1, PageSize: 24,
		Theme: &Theme{Photo: Photo{URL: "/img/tech.webp", Alt: "tech"}},
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

func TestAProductGalleryHasNoLazyPhotographAndReservesEveryBox(t *testing.T) {
	t.Parallel()
	view := ProductView{Slug: "buds", Name: "Buds"}
	for i := range 3 {
		view.Images = append(view.Images, ProductImage{
			URL: fmt.Sprintf("/img/%d.webp", i), Alt: "buds", Width: 1600, Height: 1200,
		})
	}
	page := renderProduct(t, &view, i18n.ZhHant)

	assertEveryImageReservesItsBox(t, page)
	if strings.Contains(page, `loading="lazy"`) {
		t.Error("a gallery photograph or thumbnail is lazy: choosing a thumbnail would start its download")
	}
	if got := strings.Count(page, `fetchpriority="high"`); got != 1 {
		t.Errorf("%d high-priority photographs, want the first shot's alone", got)
	}
}
