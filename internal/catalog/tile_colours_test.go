package catalog

import (
	"slices"
	"testing"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/tile"
)

// TestTilesColours holds that the tile builders carry the colours of their
// rows, and which of them are sold out.
//
// The deals builder's row is campaign's TestDealTilesCarryTheColoursOfTheirRows.
func TestTilesColours(t *testing.T) {
	t.Parallel()
	colours := []string{"#111111", "#222222"}
	soldOut := []string{"", "Walnut"}

	tests := []struct {
		name string
		got  tile.Tile
	}{
		{"tiles", tiles([]db.CategoryListingRow{{Colours: colours, ColoursSoldOut: soldOut}}, nil)[0]},
		{"searchTiles", searchTiles([]db.SearchProductsRow{{Colours: colours, ColoursSoldOut: soldOut}}, nil)[0]},
	}
	for _, tc := range tests {
		if !slices.Equal(tc.got.Colours, colours) {
			t.Errorf("%s: colours = %v, want %v", tc.name, tc.got.Colours, colours)
		}
		if !slices.Equal(tc.got.ColoursSoldOut, soldOut) {
			t.Errorf("%s: colours sold out = %q, want %q", tc.name, tc.got.ColoursSoldOut, soldOut)
		}
	}
}
