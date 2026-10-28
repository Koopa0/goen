//go:build integration

package orders_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/order/returns"
	"github.com/koopa0/goen/internal/payment/refund"
)

// TestStore_OrderUnsentRefundedUnits: units refunded unsent are no longer owed
// a parcel. A unit an unsent refund claims is no longer owed a parcel the
// moment the refund is approved: the ship form, the picking slip and the
// picking totals all stop asking for it.
func TestStore_OrderUnsentRefundedUnits(t *testing.T) {
	owner := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, owner)
	adminPool := admintest.AdminRolePool(t, owner)
	pending := admintest.Refunder{State: refund.Pending}
	desk := admintest.OrderStore(adminPool, pending, nil, admintest.DisabledInvoiceWriter{})
	o := admintest.PartlyShippedOrder(t, owner, admintest.TwoLineOrderTotal)

	owed := func(t *testing.T, want int32) {
		t.Helper()
		view, err := desk.Order(ctx, o.Number)
		if err != nil {
			t.Fatalf("read the order page: %v", err)
		}
		if len(view.Shippable) != 1 || view.Shippable[0].Remaining != want {
			t.Errorf("the ship form offers %+v, want B × %d", view.Shippable, want)
		}
		picking, err := desk.Picking(ctx, "")
		if err != nil {
			t.Fatalf("read the picking queue: %v", err)
		}
		var slip, total int64
		for _, s := range picking.Slips {
			if s.Number == o.Number {
				for _, l := range s.Lines {
					slip += int64(l.Quantity)
				}
			}
		}
		for _, l := range picking.Totals {
			total += l.Remaining
		}
		if slip != int64(want) || total != int64(want) {
			t.Errorf("picking slip asks for %d and the totals for %d, want %d", slip, total, want)
		}
	}
	owed(t, 2)

	s := refunds.NewStore(adminPool, pending, nil)
	if err := s.RefundUnsent(ctx, o.Number, &refunds.UnsentRequest{
		Units: map[uuid.UUID]int32{o.LineB: 1}, Reason: string(returns.UnsentLost), Note: "物流遺失",
	}); !errors.Is(err, refunds.ErrUnsettled) {
		t.Fatalf("open the refund with the card refund pending: %v", err)
	}
	owed(t, 1)
}

// TestStore_FillParcelSchemaRefusal: a parcel line the schema refuses is a
// quantity notice. The schema refuses a parcel line carrying a unit an unsent
// refund took, which the ship form no longer offers; if one reaches it all the
// same, staff meet the quantity notice and not a failed page.
func TestStore_FillParcelSchemaRefusal(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	adminPool := admintest.AdminRolePool(t, pool)
	pending := admintest.Refunder{State: refund.Pending}
	o := admintest.PartlyShippedOrder(t, pool, admintest.TwoLineOrderTotal)
	if err := refunds.NewStore(adminPool, pending, nil).RefundUnsent(ctx, o.Number, &refunds.UnsentRequest{
		Units: map[uuid.UUID]int32{o.LineB: 2}, Reason: string(returns.UnsentDamaged), Note: "外盒破損",
	}); !errors.Is(err, refunds.ErrUnsettled) {
		t.Fatalf("open the refund with the card refund pending: %v", err)
	}
	var shipment, hold uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, 'black_cat', 'REFUSED-' || $2) RETURNING id`, o.ID, uuid.NewString()[:8]).Scan(&shipment); err != nil {
		t.Fatalf("record a parcel: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT id FROM inventory_reservations WHERE order_id = $1 AND variant_id = $2 AND state = 'held'`,
		o.ID, o.VariantB).Scan(&hold); err != nil {
		t.Fatalf("read B's hold: %v", err)
	}
	store := admintest.OrderStore(adminPool, pending, nil, admintest.DisabledInvoiceWriter{})
	err := store.FillParcel(ctx, o.ID, shipment, o.LineB, hold, 1)
	if !errors.Is(err, orders.ErrQuantity) || !db.IsConstraint(err, "shipment_within_purchase") {
		t.Fatalf("a parcel line for a refunded unit: %v, want the quantity notice from shipment_within_purchase", err)
	}
}
