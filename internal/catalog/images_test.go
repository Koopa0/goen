package catalog

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/ui/pages"
)

// The image helpers of ui/pages' imageloading_test.go, which the moved department
// test calls; the home page's test there keeps the originals.

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

func tiledShelf(n int) []pages.ProductTile {
	tiles := make([]pages.ProductTile, 0, n)
	for i := range n {
		slug := fmt.Sprintf("p%d", i)
		tiles = append(tiles, pages.ProductTile{
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
