//go:build integration

package cart_test

import (
	"testing"
	"time"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/ui/pages"
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

// returnOf records a return of every unit of the order, as the database holds one at the given status.
func returnOf(t *testing.T, number, status string, beforeShipment bool) string {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer pgtx.Rollback(ctx, tx)
	if _, err = tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatalf("relax triggers: %v", err)
	}
	var id string
	if err = tx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, status, reason, decided_at, goods_refund_cents, shipping_refund_cents,
		                             card_refund_cents, credit_refund_cents, before_shipment)
		SELECT o.id, $2, 'test', now() - interval '2 days',
		       (SELECT sum(unit_price_cents * quantity) FROM order_lines WHERE order_id = o.id), 0,
		       (SELECT sum(unit_price_cents * quantity) FROM order_lines WHERE order_id = o.id), 0, $3
		FROM orders o WHERE o.order_number = $1 RETURNING id`, number, status, beforeShipment).Scan(&id); err != nil {
		t.Fatalf("insert return: %v", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity, received_quantity, restocked_quantity)
		SELECT order_id, $2, id, quantity, quantity, quantity FROM order_lines
		WHERE order_id = (SELECT id FROM orders WHERE order_number = $1)`, number, id); err != nil {
		t.Fatalf("insert return lines: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return id
}

// TestOnlyAnApprovedOrCompletedReturnAfterShipmentMakesAnOrderReturned holds what the order page calls fully
// returned: the refund is paid when the return is approved, and a refund before shipment returned nothing.
func TestOnlyAnApprovedOrCompletedReturnAfterShipmentMakesAnOrderReturned(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	s := cart.NewStore(pool)

	approved := placeUnpaidOrderFor(t, s, "approved@example.com")
	returnOf(t, approved, "approved", false)
	view, err := s.Order(ctx, approved)
	if err != nil || view.Returned == nil {
		t.Fatalf("an approved return: Returned = %+v, err %v; want one", view.Returned, err)
	}
	if got, want := view.StateKey(), i18n.KeyStatusRefunded; got != want {
		t.Errorf("an approved return: StateKey = %q, want %q", got, want)
	}
	if got := view.PaymentState(); got != pages.PaymentRefunded {
		t.Errorf("an approved return: PaymentState = %q, want %q", got, pages.PaymentRefunded)
	}
	if view.CanRequestReturn() {
		t.Error("an approved return of every unit still offers a return")
	}

	cancelled := placeUnpaidOrderFor(t, s, "cancelled@example.com")
	returnOf(t, cancelled, "completed", true)
	if view, err := s.Order(ctx, cancelled); err != nil || view.Returned != nil {
		t.Errorf("a refund before shipment: Returned = %+v, err %v; want nil", view.Returned, err)
	}

	paid := placeUnpaidOrderFor(t, s, "paid@example.com")
	view, err = s.Order(ctx, paid)
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	session := "cs_returned_" + paid
	if _, err = pool.Exec(ctx, `SELECT open_payment(id, $2, $3) FROM orders WHERE order_number = $1`, paid, session, view.OwedCents); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err = pool.Exec(ctx, `SELECT capture_payment($1, $2, NULL, NULL)`, session, view.OwedCents); err != nil {
		t.Fatalf("capture payment: %v", err)
	}
	returnID := returnOf(t, paid, "completed", false)
	var succeededAt time.Time
	if err = pool.QueryRow(ctx, `
		INSERT INTO refunds (payment_id, return_request_id, request_key, status, amount_cents, reason, provider_ref, succeeded_at)
		SELECT p.id, r.id, 'return:' || r.id, 'succeeded', r.card_refund_cents, 'test', 're_' || r.id, now() - interval '1 day'
		FROM return_requests r JOIN payments p ON p.order_id = r.order_id AND p.status = 'succeeded'
		WHERE r.id = $1 RETURNING succeeded_at`, returnID).Scan(&succeededAt); err != nil {
		t.Fatalf("insert refund: %v", err)
	}
	view, err = s.Order(ctx, paid)
	if err != nil || view.Returned == nil {
		t.Fatalf("a completed return after shipment: Returned = %+v, err %v; want one", view.Returned, err)
	}
	if !view.Returned.At.Equal(succeededAt) {
		t.Errorf("Returned.At = %v, want the refund's succeeded_at %v", view.Returned.At, succeededAt)
	}
}
