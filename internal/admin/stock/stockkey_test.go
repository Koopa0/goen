package stock

import (
	"testing"

	"github.com/koopa0/goen/internal/db"
)

// TestVariant_ReceiveKey: two renderings of one stock row carry different keys.
//
// The receive form's key is spent for good in the ledger, so two renderings of
// the same row at the same stock level must not share one: stock returns to an
// earlier figure through sales and holds, and the form rendered then would
// carry a key the ledger already holds.
func TestVariant_ReceiveKey(t *testing.T) {
	t.Parallel()
	row := db.AdminVariantsRow{SKU: "KEY-1", StockQuantity: 11}
	first, second := variantRow(&row), variantRow(&row)
	if first.ReceiveKey() == second.ReceiveKey() {
		t.Errorf("both renderings carry %q", first.ReceiveKey())
	}
}

// TestMovementsView_AdjustKey: a correction form's key is its rendering's, and
// a refused form hands its own key back.
func TestMovementsView_AdjustKey(t *testing.T) {
	t.Parallel()
	first, second := MovementsView{SKU: "KEY-1", FormID: "a"}, MovementsView{SKU: "KEY-1", FormID: "b"}
	if first.AdjustKey() == second.AdjustKey() {
		t.Errorf("both renderings carry %q", first.AdjustKey())
	}
	refused := MovementsView{SKU: "KEY-1", FormID: "c", AdjustmentKey: "adj:KEY-1:a"}
	if got := refused.AdjustKey(); got != "adj:KEY-1:a" {
		t.Errorf("a refused correction renders key %q, want the one it was posted with", got)
	}
}
