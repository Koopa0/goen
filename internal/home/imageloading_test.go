package home

import (
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

var imgTag = regexp.MustCompile(`<img\b[^>]*>`)

func imageTags(html string) []string { return imgTag.FindAllString(html, -1) }

// TestPageImageLoading holds that the home page asks for one photograph at
// high priority, the dark tile's, which is the page's largest paint; that the
// shelf under it loads its first two rows at once, and the rail all of its
// cards, which lie beside the window in a scroller; that everything further
// down waits to be scrolled to; and that every product photograph reserves its
// box, so nothing moves when it lands.
func TestPageImageLoading(t *testing.T) {
	t.Parallel()
	view := fullView()
	view.Shelf.Tiles = someTiles(10)
	page := renderComponent(t, i18n.WithLocale(t.Context(), i18n.ZhHant), Page(frame.Page{}, view))

	var shelf, rail, rest []string
	for _, tag := range imageTags(page) {
		switch {
		case strings.Contains(tag, `class="goen-bento__photo"`):
			if !strings.Contains(tag, `fetchpriority="high"`) || strings.Contains(tag, `loading="lazy"`) {
				t.Errorf("the dark tile's photograph is not the one at high priority: %s", tag)
			}
		case strings.Contains(tag, `class="goen-bento__lifephoto"`):
			if strings.Contains(tag, `loading="lazy"`) {
				t.Errorf("the lifestyle tile, in the first screen, waits: %s", tag)
			}
		case strings.Contains(tag, `class="goen-tile__img"`) && len(shelf) < len(view.Shelf.Tiles):
			shelf = append(shelf, tag)
		case strings.Contains(tag, `class="goen-tile__img"`) && len(rail) < len(view.Rail.Tiles):
			rail = append(rail, tag)
		default:
			rest = append(rest, tag)
		}
		if strings.Contains(tag, `class="goen-tile__img"`) && (!strings.Contains(tag, ` width=`) || !strings.Contains(tag, ` height=`)) {
			t.Errorf("a product photograph without width and height: %s", tag)
		}
	}
	if got := strings.Count(page, `fetchpriority="high"`); got != 1 {
		t.Errorf("%d high-priority photographs, want the hero's alone", got)
	}
	if len(shelf) != 10 {
		t.Fatalf("%d shelf photographs, want 10", len(shelf))
	}
	// The first eight, the tile's eagerTiles, which stays unexported in ui/tile.
	for i, tag := range shelf {
		if got, want := strings.Contains(tag, `loading="lazy"`), i >= 8; got != want {
			t.Errorf("shelf photograph %d lazy = %t, want %t: %s", i, got, want, tag)
		}
	}
	if len(rail) != len(view.Rail.Tiles) {
		t.Fatalf("%d rail photographs, want %d", len(rail), len(view.Rail.Tiles))
	}
	for i, tag := range rail {
		if strings.Contains(tag, `loading="lazy"`) || strings.Contains(tag, "fetchpriority") {
			t.Errorf("rail photograph %d waits to be scrolled to, or asks for priority: %s", i, tag)
		}
	}
	if len(rest) == 0 {
		t.Fatal("nothing drawn under the shelf")
	}
	for _, tag := range rest {
		if !strings.Contains(tag, `loading="lazy"`) {
			t.Errorf("a photograph under the shelf loads at once: %s", tag)
		}
	}
}
