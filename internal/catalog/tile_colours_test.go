package catalog

import (
	"slices"
	"testing"

	"github.com/koopa0/goen/internal/db"
)

// The deals builder's row is campaign's TestDealTilesCarryTheColoursOfTheirRows.
func TestTileBuildersCarryTheColoursOfTheirRows(t *testing.T) {
	t.Parallel()
	colours := []string{"#111111", "#222222"}

	tests := []struct {
		name string
		got  []string
	}{
		{"tiles", tiles([]db.CategoryListingRow{{Colours: colours}}, nil)[0].Colours},
		{"searchTiles", searchTiles([]db.SearchProductsRow{{Colours: colours}}, nil)[0].Colours},
	}
	for _, tc := range tests {
		if !slices.Equal(tc.got, colours) {
			t.Errorf("%s: colours = %v, want %v", tc.name, tc.got, colours)
		}
	}
}
