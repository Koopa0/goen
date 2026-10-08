//go:build integration

package cart_test

import (
	"fmt"
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

// asWritten runs one statement with the triggers off, as the payout's own writes leave the rows, and scans the row
// it returns into dest when dest is given.
func asWritten(t *testing.T, dest []any, query string, args ...any) {
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
	if len(dest) == 0 {
		_, err = tx.Exec(ctx, query, args...)
	} else {
		err = tx.QueryRow(ctx, query, args...).Scan(dest...)
	}
	if err != nil {
		t.Fatalf("write %q: %v", query, err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// payFor captures the order's whole amount, so a card refund has a payment to come from.
func payFor(t *testing.T, s *cart.Store, number string) {
	t.Helper()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	session := "cs_returned_" + number
	if _, err = pool.Exec(ctx, `SELECT open_payment(id, $2, $3) FROM orders WHERE order_number = $1`, number, session, view.OwedCents); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err = pool.Exec(ctx, `SELECT capture_payment($1, $2, NULL, NULL)`, session, view.OwedCents); err != nil {
		t.Fatalf("capture payment: %v", err)
	}
}

// TestAnOrderReadsRefundedOnlyOnceItsReturnsRefundHasSettled holds what the order page calls fully returned. An
// approved return has only started its payout: until its card refund has succeeded and the payout has written the
// refunded event, the money has not been sent and the order keeps its own words. A return with nothing to send
// back, or a completed one, has settled; a refund before shipment returned nothing.
func TestAnOrderReadsRefundedOnlyOnceItsReturnsRefundHasSettled(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	s := cart.NewStore(pool)

	for i, tc := range []struct {
		name   string
		status string
		card   string // the card refund's status, or "" for none asked for yet
		zero   bool   // the return sends nothing back
		event  bool   // the payout wrote the refunded event
		want   bool
	}{
		{"approved, its card refund not asked for", "approved", "", false, false, false},
		{"approved, its card refund pending", "approved", "pending", false, false, false},
		{"approved, its card refund failed", "approved", "failed", false, false, false},
		{"approved, its card refund succeeded but the payout unfinished", "approved", "succeeded", false, false, false},
		{"approved and refunded", "approved", "succeeded", false, true, true},
		{"approved with nothing to send back", "approved", "", true, false, true},
		{"completed", "completed", "succeeded", false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			number := placeUnpaidOrderFor(t, s, fmt.Sprintf("settled-%d@example.com", i))
			payFor(t, s, number)
			returnID := returnOf(t, number, tc.status, false)
			if tc.zero {
				asWritten(t, nil, `
					UPDATE return_requests SET goods_refund_cents = 0, card_refund_cents = 0, credit_refund_cents = 0
					WHERE id = $1`, returnID)
			}
			if tc.card != "" {
				asWritten(t, nil, `
					INSERT INTO refunds (payment_id, return_request_id, request_key, status, amount_cents, reason,
					                     provider_ref, succeeded_at, failed_at)
					SELECT p.id, r.id, 'return:' || r.id, $2::text, r.card_refund_cents, 'test', 're_' || r.id,
					       CASE WHEN $2::text = 'succeeded' THEN now() - interval '1 day' END,
					       CASE WHEN $2::text = 'failed' THEN now() - interval '1 day' END
					FROM return_requests r JOIN payments p ON p.order_id = r.order_id AND p.status = 'succeeded'
					WHERE r.id = $1`, returnID, tc.card)
			}
			var wantAt time.Time
			if tc.event {
				asWritten(t, []any{&wantAt}, `
					INSERT INTO order_events (order_id, kind, return_request_id, occurred_at)
					SELECT order_id, 'refunded', id, now() - interval '12 hours' FROM return_requests WHERE id = $1
					RETURNING occurred_at`, returnID)
			} else if err := pool.QueryRow(ctx, `SELECT decided_at FROM return_requests WHERE id = $1`,
				returnID).Scan(&wantAt); err != nil {
				t.Fatalf("read decision: %v", err)
			}

			view, err := s.Order(ctx, number)
			if err != nil {
				t.Fatalf("order: %v", err)
			}
			if !tc.want {
				if view.Returned != nil {
					t.Errorf("Returned = %+v, want nil", view.Returned)
				}
				if got := view.StateKey(); got == i18n.KeyStatusRefunded {
					t.Errorf("StateKey = %q, want the order's own state", got)
				}
				if got := view.PaymentState(); got != pages.PaymentPaid {
					t.Errorf("PaymentState = %q, want %q", got, pages.PaymentPaid)
				}
				return
			}
			if view.Returned == nil {
				t.Fatal("Returned = nil, want one")
			}
			if got, want := view.StateKey(), i18n.KeyStatusRefunded; got != want {
				t.Errorf("StateKey = %q, want %q", got, want)
			}
			if got := view.PaymentState(); got != pages.PaymentRefunded {
				t.Errorf("PaymentState = %q, want %q", got, pages.PaymentRefunded)
			}
			if view.CanRequestReturn() {
				t.Error("an order refunded in full still offers a return")
			}
			if !view.Returned.At.Equal(wantAt) {
				t.Errorf("Returned.At = %v, want the day it was refunded, %v", view.Returned.At, wantAt)
			}
		})
	}

	cancelled := placeUnpaidOrderFor(t, s, "cancelled@example.com")
	returnOf(t, cancelled, "completed", true)
	if refunded, err := s.Order(ctx, cancelled); err != nil || refunded.Returned != nil {
		t.Errorf("a refund before shipment: Returned = %+v, err %v; want nil", refunded.Returned, err)
	}
}
