package campaign

import (
	"slices"
	"testing"

	"github.com/koopa0/goen/internal/db"
)

// The other builders' rows are catalog's TestTileBuildersCarryTheColoursOfTheirRows.
func TestDealTilesCarryTheColoursOfTheirRows(t *testing.T) {
	t.Parallel()
	colours := []string{"#111111", "#222222"}
	if got := dealTiles([]db.DealProductsRow{{Colours: colours}})[0].Colours; !slices.Equal(got, colours) {
		t.Errorf("dealTiles: colours = %v, want %v", got, colours)
	}
}
