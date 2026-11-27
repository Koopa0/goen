package catalog

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/tile"
)

// TestNormaliseSlugs holds that normaliseSlugs bounds the list and reports what
// it dropped.
func TestNormaliseSlugs(t *testing.T) {
	t.Parallel()
	got, dropped := normaliseSlugs([]string{"a", "", "a", "b", "c", "d", "e"})
	if len(got) != 5 || dropped {
		t.Errorf("five distinct slugs: got %v dropped=%v", got, dropped)
	}
	got, dropped = normaliseSlugs([]string{"a", "b", "c", "d", "e", "f"})
	if len(got) != MaxCompare || !dropped {
		t.Errorf("six slugs: got %v dropped=%v, want %d and true", got, dropped, MaxCompare)
	}
}

// TestCompareCandidates holds that the picker offers only products not already
// compared.
//
// An "add" for a product already in the comparison is a control that reloads
// the page unchanged.
func TestCompareCandidates(t *testing.T) {
	t.Parallel()
	view := CompareView{Products: []CompareProduct{{Slug: "a"}, {Slug: "b"}}}
	found := []tile.Tile{{Slug: "a", Name: "A"}, {Slug: "c", Name: "C", Brand: "B"}}
	got := compareCandidates(found, view)
	if len(got) != 1 || got[0].Slug != "c" || got[0].Name != "C" {
		t.Errorf("candidates = %+v, want only c", got)
	}
}

// TestTilesComparable holds that tiles and searchTiles hand a card the compare
// toggle only for a category that offers comparison.
//
// The listing and the search read the same set, and a category outside it gets
// no control.
func TestTilesComparable(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	yes, no := uuid.New(), uuid.New()
	offers := map[uuid.UUID]bool{yes: true}

	listing := tiles([]db.CategoryListingRow{{Slug: "a", Name: "A", CategoryID: yes}, {Slug: "b", CategoryID: no}}, offers)
	search := searchTiles([]db.SearchProductsRow{{Slug: "a", Name: "A", CategoryID: yes}, {Slug: "b", CategoryID: no}}, offers)
	for name, got := range map[string][]tile.Tile{"listing": listing, "search": search} {
		if len(got) != 2 || got[0].CompareControl == nil || got[1].CompareControl != nil {
			t.Errorf("%s: compare controls = %v %v, want a toggle and none", name, got[0].CompareControl != nil, got[1].CompareControl != nil)
			continue
		}
		html := renderComponent(t, ctx, got[0].CompareControl)
		if !strings.Contains(html, `<input type="hidden" name="slug" value="a">`) || !strings.Contains(html, `<span class="goen-sr-only"> A</span>`) {
			t.Errorf("%s: the control is not the toggle for a, named A: %s", name, html)
		}
	}
	if tiles([]db.CategoryListingRow{{Slug: "a", CategoryID: yes}}, nil)[0].CompareControl != nil {
		t.Error("no category offers comparison and a tile still carries the toggle")
	}
}

// TestSuggestionTiles holds that a suggestion is drawn as a product tile.
//
// The name, brand, price and photograph are what the one-product comparison
// shows for each.
func TestSuggestionTiles(t *testing.T) {
	t.Parallel()
	got := suggestionTiles([]db.CompareSuggestionsRow{
		{Slug: "b", Name: "Bee", Brand: "B", MinPriceCents: 1234, PriceVaries: true, ImageBundledKey: "pixelight-9-pro-01.webp", ImageAlt: "alt"},
	})
	if len(got) != 1 || got[0].Slug != "b" || got[0].Name != "Bee" || got[0].Brand != "B" ||
		got[0].PriceCents != 1234 || !got[0].PriceVaries || got[0].ImageURL == "" || got[0].ImageAlt != "alt" {
		t.Errorf("suggestionTiles = %+v", got)
	}
}
