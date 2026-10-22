package home

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/hero"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/tile"
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

func tiledShelf(n int) []tile.Tile {
	tiles := make([]tile.Tile, 0, n)
	for i := range n {
		slug := fmt.Sprintf("p%d", i)
		tiles = append(tiles, tile.Tile{
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
	view := View{
		Slides: []hero.Slide{
			{Layout: hero.LayoutPhoto, Photo: components.Photo{URL: "/img/hero.webp", Alt: "hero"}, PhotoWidth: 1600, PhotoHeight: 600, Title: "A"},
			{Layout: hero.LayoutPhoto, Photo: components.Photo{URL: "/img/hero2.webp", Alt: "hero"}, PhotoWidth: 1600, PhotoHeight: 600, Title: "B"},
		},
		Categories: []Category{
			{Slug: "a", Name: "A", Photo: components.Photo{URL: "/img/a.webp"}},
			{Slug: "b", Name: "B", Photo: components.Photo{URL: "/img/b.webp"}},
		},
		Row: ProductRow{Title: "Row", Href: "/s/row", Tiles: tiledShelf(10)},
	}
	page := renderIn(t, i18n.ZhHant, Page(layouts.Page{}, view))

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
	// The first eight, the tile's eagerTiles, which stays unexported in ui/tile.
	for i, tag := range tiles {
		wantLazy := i >= 8
		if got := strings.Contains(tag, `loading="lazy"`); got != wantLazy {
			t.Errorf("tile %d lazy = %t, want %t: %s", i, got, wantLazy, tag)
		}
	}
	if got := strings.Count(page, `fetchpriority="high"`); got != 1 {
		t.Errorf("%d high-priority photographs, want the hero's alone", got)
	}
}
