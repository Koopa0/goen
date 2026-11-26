package catalog

import (
	"testing"

	"github.com/koopa0/goen/internal/ui/tile"
)

// TestShelf_Empty holds that an empty shelf depends on the total of matches,
// not on the page's products; and that a search that matched nothing is told
// apart from one whose filters left none of its matches.
func TestShelf_Empty(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		total    int64
		products []tile.Tile
		want     bool
	}{
		{"past last page", 3, nil, false},
		{"no matches", 0, nil, true},
		{"populated page", 1, []tile.Tile{{Slug: "match"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := (&Shelf{Total: tc.total, Products: tc.products}).Empty(); got != tc.want {
				t.Errorf("Empty() = %v, want %v", got, tc.want)
			}
		})
	}
	if (&SearchView{}).Unmatched() {
		t.Error("an unsubmitted search is a search that matched nothing")
	}
	if !(&SearchView{Query: "zz"}).Unmatched() {
		t.Error("a search with no matches is not told as one")
	}
	if (&SearchView{Query: "pro", Matches: 4}).Unmatched() {
		t.Error("a search whose filters left nothing is told as one that matched nothing")
	}
}
