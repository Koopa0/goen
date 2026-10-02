package stock

import (
	"testing"

	"github.com/koopa0/goen/internal/db"
)

// The adjust form's key is spent for good in the ledger, so two renderings of
// the same row at the same stock level must not share one: stock returns to an
// earlier figure through sales and holds, and the form rendered then would
// carry a key the ledger already holds.
func TestTwoRenderingsOfOneStockRowCarryDifferentKeys(t *testing.T) {
	t.Parallel()
	row := db.AdminVariantsRow{SKU: "KEY-1", StockQuantity: 11}
	first, second := variantRow(&row), variantRow(&row)
	if first.AdjustKey() == second.AdjustKey() {
		t.Errorf("both renderings carry %q", first.AdjustKey())
	}
}
