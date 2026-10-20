//go:build integration

package customers_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/customers"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/components"
)

func TestCustomerOrderStatusSaysRefundedOnceTheWholeOrdersRefundHasSettled(t *testing.T) {
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
			number, lineID, userID := admintest.WarrantableOrder(t, pool, 2)
			var orderID uuid.UUID
			if err := pool.QueryRow(t.Context(), `SELECT id FROM orders WHERE order_number=$1`, number).Scan(&orderID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), `UPDATE orders SET fulfillment_status='delivered' WHERE id=$1`, orderID); err != nil {
				t.Fatal(err)
			}
			if tc.quantity > 0 {
				admintest.ReturnWithStatus(t, pool, orderID, lineID, tc.quantity, tc.status, tc.settlement)
			}
			staff, _ := admintest.StaffContext(t, pool)
			store := customers.NewStore(admintest.AdminRolePool(t, pool))
			for _, locale := range i18n.Locales() {
				ctx := i18n.WithLocale(staff, locale)
				profile, err := store.Profile(ctx, userID.String())
				if err != nil {
					t.Fatal(err)
				}
				if len(profile.Recent) != 1 {
					t.Fatalf("recent orders=%d, want 1", len(profile.Recent))
				}
				wantText := i18n.T(ctx, i18n.KeyStatusDelivered)
				wantIntent := components.IntentDone
				if tc.wantReturned {
					wantText = i18n.T(ctx, i18n.KeyStatusRefunded)
					wantIntent = components.IntentNeutral
				}
				recent := profile.Recent[0]
				if recent.StatusText != wantText || recent.StatusIntent != wantIntent {
					t.Errorf("%s recent order status=%q intent=%q, want %q %q", locale.Tag(), recent.StatusText, recent.StatusIntent, wantText, wantIntent)
				}
				if recent.Status != order.FulfillmentDelivered {
					t.Errorf("stored fulfillment=%s, want delivered", recent.Status)
				}
			}
		})
	}
}
