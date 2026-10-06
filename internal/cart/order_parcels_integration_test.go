//go:build integration

package cart_test

import (
	"testing"
	"time"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgtx"
)

// TestTheParcelCarriesTheDatabasesLastDays holds that the grid's two ends are the ones the return decision
// enforces: asked on each end and the day after it, return_line_policy_window reads within, goodwill and after
// where the page draws the mark and the end of the grid.
func TestTheParcelCarriesTheDatabasesLastDays(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	s := cart.NewStore(pool)
	number := placeUnpaidOrderFor(t, s, "parcel@example.com")

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer pgtx.Rollback(ctx, tx)
	// The order is still pending, which no shipment may be recorded against.
	if _, err = tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatalf("relax triggers: %v", err)
	}
	var shipmentID string
	if err = tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number, shipped_at, delivered_at)
		SELECT id, 'black_cat', 'PARCEL' || order_number, now() - interval '4 days', now() - interval '3 days'
		FROM orders WHERE order_number = $1 RETURNING id`, number).Scan(&shipmentID); err != nil {
		t.Fatalf("insert shipment: %v", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		SELECT order_id, $2, id, quantity FROM order_lines WHERE order_id = (SELECT id FROM orders WHERE order_number = $1)`,
		number, shipmentID); err != nil {
		t.Fatalf("insert shipment line: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if len(view.Shipments) != 1 || !view.Shipments[0].Delivered() {
		t.Fatalf("shipments = %+v, want one delivered parcel", view.Shipments)
	}
	got := view.Shipments[0]
	if len(got.Lines) != 1 || len(view.Unshipped) != 0 {
		t.Errorf("parcel lines = %d, unshipped = %d; want the one line in the parcel", len(got.Lines), len(view.Unshipped))
	}

	for _, tt := range []struct {
		name string
		day  time.Time
		want string
	}{
		{"the last day of the right to cancel", got.RescissionEnds, "within"},
		{"the day after it", got.RescissionEnds.AddDate(0, 0, 1), "goodwill"},
		{"the end of the grid", got.GoodwillEnds, "goodwill"},
		{"the day after the grid", got.GoodwillEnds.AddDate(0, 0, 1), "after"},
	} {
		// Midday in Taipei, the shop's day.
		requested := time.Date(tt.day.Year(), tt.day.Month(), tt.day.Day(), 4, 0, 0, 0, time.UTC)
		var window string
		if err := pool.QueryRow(ctx, `SELECT return_line_policy_window($1, delivered_at) FROM order_shipments WHERE id = $2`,
			requested, shipmentID).Scan(&window); err != nil {
			t.Fatalf("%s: read the window: %v", tt.name, err)
		}
		if window != tt.want {
			t.Errorf("%s (%s): return_line_policy_window = %q, want %q", tt.name, tt.day.Format(time.DateOnly), window, tt.want)
		}
	}
}
