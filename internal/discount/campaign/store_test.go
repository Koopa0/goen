package campaign

import (
	"slices"
	"testing"

	"github.com/koopa0/goen/internal/db"
)

// The other builders' rows are catalog's TestTilesColours.
func TestDealTilesCarryTheColoursOfTheirRows(t *testing.T) {
	t.Parallel()
	colours := []string{"#111111", "#222222"}
	soldOut := []string{"", "Walnut"}
	got := dealTiles([]db.DealProductsRow{{Colours: colours, ColoursSoldOut: soldOut}})[0]
	if !slices.Equal(got.Colours, colours) {
		t.Errorf("dealTiles: colours = %v, want %v", got.Colours, colours)
	}
	if !slices.Equal(got.ColoursSoldOut, soldOut) {
		t.Errorf("dealTiles: colours sold out = %q, want %q", got.ColoursSoldOut, soldOut)
	}
}
