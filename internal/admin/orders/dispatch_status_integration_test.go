//go:build integration

package orders_test

import (
	"context"
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
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
)

// TestADispatchTheOrderMovedPastIsRefused: the dispatch reads the order's status
// without a lock, so another staff member can complete the order before the
// parcel is recorded. shipment_order_in_fulfilment refuses it under the order's
// lock, and that is a refusal staff can read with the form still filled in.
// Completion stands for every status the trigger refuses: cancelling instead
// reaches the same branch, and a paid order can only be cancelled after a
// settled refund before shipment.
func TestADispatchTheOrderMovedPastIsRefused(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	number := shippableOrder(t, "zh-Hant")
	if err := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil).Ship(ctx, number,
		orders.Dispatch{Carrier: "black_cat", Tracking: "FIRST-" + uuid.NewString()[:8]},
		uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
		t.Fatalf("first parcel: %v", err)
	}

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
	held := reservations()

	completer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the completing transaction: %v", err)
	}
	defer func() { _ = completer.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = completer.Exec(ctx, `
		UPDATE orders SET fulfillment_status = 'completed', completed_at = now()
		WHERE order_number = $1`, number); err != nil {
		t.Fatalf("complete %s: %v", number, err)
	}

	name := "dispatch_status_" + uuid.NewString()[:8]
	h := admintest.OrderDesk(admintest.OrderStore(admintest.NamedPool(t, pool, name), admintest.Refunder{}, nil, nil))
	tracking := "LATE-" + uuid.NewString()[:8]
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
	if err = completer.Commit(ctx); err != nil {
		t.Fatalf("commit the completion: %v", err)
	}
	<-done

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a dispatch for an order completed under it answered %d, want 422", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{`value="` + tracking + `"`, i18n.T(ctx, i18n.KeyAdminNoticeRefused)} {
		if !strings.Contains(body, want) {
			t.Errorf("the refused dispatch is missing %q", want)
		}
	}
	var parcels, notices, shipAudits int
	if err = pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM order_shipments s JOIN orders o ON o.id = s.order_id WHERE o.order_number = $1),
		       (SELECT count(*) FROM outbox_messages WHERE topic = $2 AND dedupe_key = $3),
		       (SELECT count(*) FROM audit_events a JOIN orders o ON o.id = a.entity_id
		        WHERE o.order_number = $1 AND a.action = $4)`,
		number, outbox.TopicOrderShipped.Name(), "black_cat:"+tracking, string(audit.ActionShipOrder)).
		Scan(&parcels, &notices, &shipAudits); err != nil {
		t.Fatal(err)
	}
	if parcels != 1 {
		t.Errorf("order %s has %d parcels after the refused dispatch, want the first one only", number, parcels)
	}
	if notices != 0 {
		t.Errorf("the refused dispatch queued %d shipping notices for %s", notices, tracking)
	}
	if got := reservations(); got != held {
		t.Errorf("the refused dispatch changed the reservations of %s from %q to %q", number, held, got)
	}
	if shipAudits != 1 {
		t.Errorf("order %s has %d %s audit rows after the refused dispatch, want the first parcel's only",
			number, shipAudits, audit.ActionShipOrder)
	}
}

// waitForLockWait returns once the connection named name is waiting on a lock,
// and fails if the work it serves finishes first.
func waitForLockWait(t *testing.T, name string, done <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
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
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never waited on the order's lock", name)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
