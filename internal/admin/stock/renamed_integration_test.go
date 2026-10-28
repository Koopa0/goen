//go:build integration

package stock_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestStore_MovementsRenamedStaff: a renamed staff is named by address in the
// stock ledger. Staff A adjusts a variant's stock, then takes staff B's name.
// The movement still names A by the address nobody on staff can change, and the
// page never says B. A display name is whatever its owner last typed on
// /account/profile.
func TestStore_MovementsRenamedStaff(t *testing.T) {
	suffix := uuid.NewString()[:8]
	ctx, idA, address := admintest.NamedStaff(t, pool, "Actor "+suffix)
	colleague := "Colleague " + suffix
	admintest.NamedStaff(t, pool, colleague)
	s := stock.NewStore(pool)

	var sku string
	if err := pool.QueryRow(ctx, `
		SELECT sku FROM product_variants WHERE is_active ORDER BY position DESC LIMIT 1`).Scan(&sku); err != nil {
		t.Fatalf("read a variant: %v", err)
	}
	if err := s.Adjust(ctx, sku, 3, idA.String(), "renamed-"+uuid.NewString()); err != nil {
		t.Fatalf("A adjusts the stock: %v", err)
	}

	admintest.RenameUser(t, pool, idA, colleague)
	view, err := s.Movements(ctx, sku, admintest.DatabaseNow(t, pool), "")
	if err != nil {
		t.Fatalf("Movements: %v", err)
	}
	var found bool
	for _, m := range view.Rows {
		if m.Actor == address {
			found = true
		}
	}
	if !found {
		t.Errorf("no movement is by %q among %d rows", address, len(view.Rows))
	}
	var html bytes.Buffer
	if err := stock.Movements(frame.Page{}, &view).Render(ctx, &html); err != nil {
		t.Fatalf("render the movements page: %v", err)
	}
	if !strings.Contains(html.String(), address) {
		t.Errorf("the page does not show %q", address)
	}
	if strings.Contains(html.String(), colleague) {
		t.Errorf("the page shows the name %q the staff member took", colleague)
	}
}
