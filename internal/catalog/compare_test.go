package catalog

import (
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestNormaliseSlugsBoundsAndReportsWhatItDropped(t *testing.T) {
	t.Parallel()
	got, dropped := normaliseSlugs([]string{"a", "", "a", "b", "c", "d"})
	if len(got) != 4 || dropped {
		t.Errorf("four distinct slugs: got %v dropped=%v", got, dropped)
	}
	got, dropped = normaliseSlugs([]string{"a", "b", "c", "d", "e"})
	if len(got) != pages.MaxCompare || !dropped {
		t.Errorf("five slugs: got %v dropped=%v, want %d and true", got, dropped, pages.MaxCompare)
	}
}

// TestThePickerOffersOnlyProductsNotAlreadyCompared: an "add" for a product
// already in the comparison is a control that reloads the page unchanged.
func TestThePickerOffersOnlyProductsNotAlreadyCompared(t *testing.T) {
	t.Parallel()
	view := pages.CompareView{Products: []pages.CompareProduct{{Slug: "a"}, {Slug: "b"}}}
	found := []pages.ProductTile{{Slug: "a", Name: "A"}, {Slug: "c", Name: "C", Brand: "B"}}
	got := compareCandidates(found, view)
	if len(got) != 1 || got[0].Slug != "c" || got[0].Name != "C" {
		t.Errorf("candidates = %+v, want only c", got)
	}
}

// TestATileCarriesTheBoxOnlyForACategoryThatOffersComparison: the listing and
// the search read the same set, and a category outside it gets no box.
func TestATileCarriesTheBoxOnlyForACategoryThatOffersComparison(t *testing.T) {
	t.Parallel()
	yes, no := uuid.New(), uuid.New()
	offers := map[uuid.UUID]bool{yes: true}

	listing := tiles([]db.CategoryListingRow{{Slug: "a", CategoryID: yes}, {Slug: "b", CategoryID: no}}, offers)
	search := searchTiles([]db.SearchProductsRow{{Slug: "a", CategoryID: yes}, {Slug: "b", CategoryID: no}}, offers)
	for name, got := range map[string][]pages.ProductTile{"listing": listing, "search": search} {
		if len(got) != 2 || !got[0].Comparable || got[1].Comparable {
			t.Errorf("%s: comparable = %v %v, want true false", name, got[0].Comparable, got[1].Comparable)
		}
	}
	if tiles([]db.CategoryListingRow{{Slug: "a", CategoryID: yes}}, nil)[0].Comparable {
		t.Error("no category offers comparison and a tile still carries the box")
	}
}

// TestASuggestionIsDrawnAsAProductTile: the name, brand, price and photograph
// are what the one-product comparison shows for each.
func TestASuggestionIsDrawnAsAProductTile(t *testing.T) {
	t.Parallel()
	got := suggestionTiles([]db.CompareSuggestionsRow{
		{Slug: "b", Name: "Bee", Brand: "B", MinPriceCents: 1234, PriceVaries: true, ImageKey: "pixelight-9-pro-01.webp", ImageAlt: "alt"},
	})
	if len(got) != 1 || got[0].Slug != "b" || got[0].Name != "Bee" || got[0].Brand != "B" ||
		got[0].PriceCents != 1234 || !got[0].PriceVaries || got[0].ImageURL == "" || got[0].ImageAlt != "alt" {
		t.Errorf("suggestionTiles = %+v", got)
	}
}
