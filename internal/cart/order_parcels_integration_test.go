//go:build integration

package cart_test

import (
	"testing"
	"time"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
)

// TestTheParcelCarriesTheDatabasesLastDays holds that the order page reads both ends of the return window from
// the database: the last day of the right to cancel from return_window_ends, and the goodwill end from the same
// shop day, so Go adds neither number.
func TestTheParcelCarriesTheDatabasesLastDays(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	s := cart.NewStore(pool)
	number := placeUnpaidOrderFor(t, s, "parcel@example.com")

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // the commit below is the success path
	// The order is still pending, which no shipment may be recorded against.
	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatalf("relax triggers: %v", err)
	}
	var shipmentID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number, shipped_at, delivered_at)
		SELECT id, 'black_cat', 'PARCEL' || order_number, now() - interval '4 days', now() - interval '3 days'
		FROM orders WHERE order_number = $1 RETURNING id`, number).Scan(&shipmentID); err != nil {
		t.Fatalf("insert shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		SELECT order_id, $2, id, quantity FROM order_lines WHERE order_id = (SELECT id FROM orders WHERE order_number = $1)`,
		number, shipmentID); err != nil {
		t.Fatalf("insert shipment line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var lastDay, goodwill time.Time
	if err := pool.QueryRow(ctx, `
		SELECT return_window_ends(delivered_at), shop_day(delivered_at) + 14 FROM order_shipments WHERE id = $1`,
		shipmentID).Scan(&lastDay, &goodwill); err != nil {
		t.Fatalf("read the window: %v", err)
	}

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if len(view.Shipments) != 1 || !view.Shipments[0].Delivered() {
		t.Fatalf("shipments = %+v, want one delivered parcel", view.Shipments)
	}
	got := view.Shipments[0]
	if !got.RescissionEnds.Equal(lastDay) || !got.GoodwillEnds.Equal(goodwill) {
		t.Errorf("parcel days = %v and %v, want the database's %v and %v", got.RescissionEnds, got.GoodwillEnds, lastDay, goodwill)
	}
	if len(got.Lines) != 1 || len(view.Unshipped) != 0 {
		t.Errorf("parcel lines = %d, unshipped = %d; want the one line in the parcel", len(got.Lines), len(view.Unshipped))
	}
}
