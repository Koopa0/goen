//go:build integration

package orders_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/pgtx"
)

// TestADispatchTheOrderMovedPastIsRefused: the dispatch reads the order's status
// without a lock, so another staff member can complete or cancel the order
// before the parcel is recorded. shipment_order_in_fulfilment refuses it under
// the order's lock. The order no longer takes a dispatch, so the refusal names
// the carrier and tracking number staff typed: the parcel may already be out of
// the door.
func TestADispatchTheOrderMovedPastIsRefused(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	cases := []struct {
		name  string
		order func(t *testing.T) string
		moves []string
	}{
		{
			name: "completed",
			order: func(t *testing.T) string {
				t.Helper()
				number := shippableOrder(t, "zh-Hant")
				if err := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil).Ship(ctx, number,
					orders.Dispatch{Carrier: "black_cat", Tracking: "FIRST-" + uuid.NewString()[:8]},
					uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
					t.Fatalf("first parcel: %v", err)
				}
				return number
			},
			moves: []string{`UPDATE orders SET fulfillment_status = 'completed', completed_at = now()
				WHERE order_number = $1`},
		},
		{
			// A paid order is cancelled only once its refund has settled and its
			// invoice is voided; the refund settles here and the void is the move.
			name: "cancelled",
			order: func(t *testing.T) string {
				t.Helper()
				number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, true)
				if _, err := pool.Exec(ctx, `
					INSERT INTO invoice_documents (order_id, kind, number, amount_cents)
					VALUES ($1, 'invoice', $2, 500000)`, orderID, uuid.NewString()[:32]); err != nil {
					t.Fatalf("issue invoice: %v", err)
				}
				_, err := refunds.NewStore(pool, admintest.Refunder{}, nil).RefundBeforeShipment(ctx, number, "")
				if admintest.ConstraintName(err) != "orders_cancel_invoice_resolved" {
					t.Fatalf("refund with a live invoice = %v", err)
				}
				return number
			},
			moves: []string{
				`UPDATE invoice_documents SET status = 'voided', voided_at = now()
				 WHERE order_id = (SELECT id FROM orders WHERE order_number = $1)`,
				`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
				 WHERE order_number = $1`,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			number := tc.order(t)
			tracking := "LATE-" + uuid.NewString()[:8]

			reservations := func() string {
				t.Helper()
				var state string
				if err := pool.QueryRow(ctx, `
					SELECT coalesce(string_agg(r.id::text || ':' || r.state || ':' || r.quantity, ',' ORDER BY r.id), '')
					FROM inventory_reservations r JOIN orders o ON o.id = r.order_id
					WHERE o.order_number = $1`, number).Scan(&state); err != nil {
					t.Fatalf("read the reservations of %s: %v", number, err)
				}
				return state
			}
			written := func() (parcels, notices, shipAudits int) {
				t.Helper()
				if err := pool.QueryRow(ctx, `
					SELECT (SELECT count(*) FROM order_shipments s JOIN orders o ON o.id = s.order_id WHERE o.order_number = $1),
					       (SELECT count(*) FROM outbox_messages WHERE topic = $2 AND dedupe_key = $3),
					       (SELECT count(*) FROM audit_events a JOIN orders o ON o.id = a.entity_id
					        WHERE o.order_number = $1 AND a.action = $4)`,
					number, outbox.TopicOrderShipped.Name(), "black_cat:"+tracking, string(audit.ActionShipOrder)).
					Scan(&parcels, &notices, &shipAudits); err != nil {
					t.Fatal(err)
				}
				return parcels, notices, shipAudits
			}
			held := reservations()
			parcelsBefore, _, auditsBefore := written()

			mover, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin the moving transaction: %v", err)
			}
			defer pgtx.Rollback(ctx, mover)
			for _, move := range tc.moves {
				if _, err = mover.Exec(ctx, move, number); err != nil {
					t.Fatalf("move %s: %v", number, err)
				}
			}

			name := "dispatch_status_" + uuid.NewString()[:8]
			h := admintest.OrderDesk(admintest.OrderStore(admintest.NamedPool(t, pool, name), admintest.Refunder{}, nil, nil))
			form := url.Values{"carrier": {"black_cat"}, "tracking": {tracking}}
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/orders/"+number+"/ship", strings.NewReader(form.Encode()))
			req.SetPathValue("number", number)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				defer close(done)
				admintest.BackOffice.RequireStaff(h.Ship)(w, req)
			}()
			waitForLockWait(t, name, done)
			if err = mover.Commit(ctx); err != nil {
				t.Fatalf("commit the move: %v", err)
			}
			<-done

			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("a dispatch for an order %s under it answered %d, want 422", tc.name, w.Code)
			}
			want := fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminDispatchRefused), "black_cat", tracking)
			if body := w.Body.String(); !strings.Contains(body, want) {
				t.Errorf("the refused dispatch is missing %q", want)
			}
			parcels, notices, shipAudits := written()
			if parcels != parcelsBefore {
				t.Errorf("order %s has %d parcels after the refused dispatch, want %d", number, parcels, parcelsBefore)
			}
			if notices != 0 {
				t.Errorf("the refused dispatch queued %d shipping notices for %s", notices, tracking)
			}
			if got := reservations(); got != held {
				t.Errorf("the refused dispatch changed the reservations of %s from %q to %q", number, held, got)
			}
			if shipAudits != auditsBefore {
				t.Errorf("order %s has %d %s audit rows after the refused dispatch, want %d",
					number, shipAudits, audit.ActionShipOrder, auditsBefore)
			}
		})
	}
}

// waitForLockWait returns once the connection named name is waiting on a lock,
// and fails if the work it serves finishes first.
func waitForLockWait(t *testing.T, name string, done <-chan struct{}) {
	t.Helper()
	deadline := time.After(15 * time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		var waiting bool
		if err := pool.QueryRow(t.Context(), `
			SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			               WHERE application_name = $1 AND wait_event_type = 'Lock')`, name).
			Scan(&waiting); err == nil && waiting {
			return
		}
		select {
		case <-done:
			t.Fatalf("%s finished before it waited on the order's lock", name)
		case <-deadline:
			t.Fatalf("%s never waited on the order's lock", name)
		case <-tick.C:
		}
	}
}
