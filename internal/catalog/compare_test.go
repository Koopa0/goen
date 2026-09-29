package catalog

import (
	"testing"

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
