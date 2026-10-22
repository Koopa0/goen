//go:build integration

package warranties_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/warranties"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/warranty"
)

// TestStore_WarrantiesRefundedState: the warranty status says refunded once the whole order's refund has settled.
func TestStore_WarrantiesRefundedState(t *testing.T) {
	for _, tc := range []struct {
		name         string
		quantity     int32
		status       string
		settlement   admintest.Settlement
		wantReturned bool
	}{
		{name: "no return"},
		{name: "requested whole order", quantity: 2, status: "requested"},
		{name: "approved partial order, settled", quantity: 1, status: "approved", settlement: admintest.RefundSettled},
		{name: "approved whole order, refund not settled", quantity: 2, status: "approved", settlement: admintest.RefundUnsettled},
		{name: "approved whole order, refund settled", quantity: 2, status: "approved", settlement: admintest.RefundSettled, wantReturned: true},
		{name: "completed whole order", quantity: 2, status: "completed", settlement: admintest.ParcelReceived, wantReturned: true},
		{name: "rejected whole order", quantity: 2, status: "rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serial, number := registeredWarranty(t, "SN-"+strings.ToUpper(uuid.NewString()[:8]), 2)
			var orderID, userID, lineID uuid.UUID
			if err := pool.QueryRow(t.Context(), `SELECT o.id,o.user_id,ol.id FROM orders o JOIN order_lines ol ON ol.order_id=o.id WHERE o.order_number=$1`, number).Scan(&orderID, &userID, &lineID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), `UPDATE orders SET fulfillment_status='delivered' WHERE id=$1`, orderID); err != nil {
				t.Fatal(err)
			}
			if err := warranty.NewStore(pool).Register(t.Context(), number, lineID.String(), userID.String(), serial+"-2", 2); err != nil {
				t.Fatal(err)
			}
			if tc.quantity > 0 {
				admintest.ReturnWithStatus(t, pool, orderID, lineID, tc.quantity, tc.status, tc.settlement)
			}
			staff, _ := admintest.StaffContext(t, pool)
			store := warranties.NewStore(admintest.AdminRolePool(t, pool))
			for _, locale := range i18n.Locales() {
				ctx := i18n.WithLocale(staff, locale)
				wantText := i18n.T(ctx, i18n.KeyStatusDelivered)
				if tc.wantReturned {
					wantText = i18n.T(ctx, i18n.KeyStatusRefunded)
				}
				for _, term := range []string{serial, number} {
					found, err := store.Warranties(ctx, term)
					if err != nil {
						t.Fatal(err)
					}
					wantRows := 1
					if term == number {
						wantRows = 2
					}
					if len(found.Rows) != wantRows {
						t.Fatalf("warranties for %q=%d, want %d", term, len(found.Rows), wantRows)
					}
					for _, row := range found.Rows {
						if row.OrderStatus != wantText {
							t.Errorf("%s warranty order status=%q, want %q", locale.Tag(), row.OrderStatus, wantText)
						}
						if row.InForce == tc.wantReturned {
							t.Errorf("%s warranty in force=%t, want %t", locale.Tag(), row.InForce, !tc.wantReturned)
						}
					}
				}
			}
		})
	}
}
