//go:build integration

package orders_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/pages/admin"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/user"
)

func TestAdvanceRefusesAnUnfundedOrder(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)

	number := admintest.PlaceUnpaidOrder(t, pool)
	_, err := s.Advance(ctx, number, "picking", uuid.NullUUID{})
	if err == nil {
		t.Fatal("an unpaid order was moved into fulfilment")
	}
	if !errors.Is(err, orders.ErrRefused) {
		t.Errorf("refused with %v, want ErrRefused", err)
	}
	if _, err := s.Advance(ctx, number, "cancelled", uuid.NullUUID{}); err != nil {
		t.Errorf("cancelling a pending order was refused: %v", err)
	}
}

func TestAdvanceRefusesAnIllegalTransition(t *testing.T) {
	ctx := t.Context()
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number := admintest.PlaceUnpaidOrder(t, pool)

	if _, err := s.Advance(ctx, number, "shipped", uuid.NullUUID{}); !errors.Is(err, orders.ErrRefused) {
		t.Errorf("pending -> shipped gave %v, want ErrRefused", err)
	}
	if _, err := s.Advance(ctx, number, "teleported", uuid.NullUUID{}); !errors.Is(err, orders.ErrRefused) {
		t.Errorf("an unknown status gave %v, want ErrRefused", err)
	}
}

// TestAdvanceAnswersALockTimeoutAsAFailure: the order's row lock timing out is
// the database not answering, and telling staff the rules refused a move that
// was never evaluated sends them to look for a rule instead of retrying.
func TestAdvanceAnswersALockTimeoutAsAFailure(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	number := admintest.PlaceUnpaidOrder(t, pool)
	s := admintest.OrderStore(admintest.LockTimeoutPool(t, pool), admintest.Refunder{}, nil, nil)
	admintest.HoldRow(t, pool, `SELECT 1 FROM orders WHERE order_number = $1 FOR UPDATE`, number)

	_, err := s.Advance(ctx, number, order.FulfillmentCancelled, uuid.NullUUID{})
	if errors.Is(err, orders.ErrRefused) || !admintest.LockTimedOut(err) {
		t.Fatalf("Advance(%s) behind a held row lock = %v, want lock_not_available (55P03) and not ErrRefused", number, err)
	}

	form := url.Values{"status": {string(order.FulfillmentCancelled)}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/orders/"+number+"/status", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("number", number)
	rec := httptest.NewRecorder()
	admintest.OrderDesk(s).Advance(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("POST status behind a held row lock = %d %s, want 500", rec.Code, rec.Header().Get("Location"))
	}
}

func pickingOrderHoldingStock(t *testing.T) (number string, orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	number, orderID, _ = admintest.PendingOrderHoldingStock(t, pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin funding: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, "cs_ship_"+number); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 100000, NULL, NULL)`, "cs_ship_"+number); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("to picking: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit funding: %v", err)
	}
	return number, orderID
}

func TestShipDoesAllFourWritesOrNone(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number, orderID := pickingOrderHoldingStock(t)

	if err := s.Ship(ctx, number, orders.Dispatch{Carrier: "black_cat", Tracking: "TW1234567890"}, uuid.NullUUID{}); err != nil {
		t.Fatalf("ship: %v", err)
	}

	var status, carrier, tracking string
	if err := pool.QueryRow(ctx, `
		SELECT o.fulfillment_status, sh.carrier, sh.tracking_number
		FROM orders o JOIN order_shipments sh ON sh.order_id = o.id
		WHERE o.id = $1`, orderID).Scan(&status, &carrier, &tracking); err != nil {
		t.Fatalf("read shipment: %v", err)
	}
	if status != "shipped" {
		t.Errorf("order is %q after shipping, want shipped", status)
	}
	if carrier != "black_cat" || tracking != "TW1234567890" {
		t.Errorf("shipment is %q/%q, want the carrier and tracking that were submitted", carrier, tracking)
	}

	var held int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM inventory_reservations WHERE order_id = $1 AND state = 'held'`,
		orderID).Scan(&held); err != nil {
		t.Fatalf("read reservations: %v", err)
	}
	if held != 0 {
		t.Errorf("%d reservations are still held after dispatch; a sweeper would "+
			"return stock that has already gone out", held)
	}

	var consumed int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM inventory_reservations WHERE order_id = $1 AND state = 'consumed'`,
		orderID).Scan(&consumed); err != nil {
		t.Fatalf("read reservations: %v", err)
	}
	if consumed != 1 {
		t.Errorf("%d reservations consumed, want 1", consumed)
	}

	var shippedQty int
	if scanErr := pool.QueryRow(ctx, `
		SELECT coalesce(sum(sl.quantity), 0) FROM order_shipment_lines sl
		WHERE sl.order_id = $1`, orderID).Scan(&shippedQty); scanErr != nil {
		t.Fatalf("read shipment lines: %v", scanErr)
	}
	if shippedQty != 1 {
		t.Errorf("%d units recorded as shipped, want 1 — a shipment with no "+
			"lines cannot be reconciled against a return", shippedQty)
	}

	var kinds []string
	rows, err := pool.Query(ctx,
		`SELECT kind FROM order_events WHERE order_id = $1 ORDER BY occurred_at, id`, orderID)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if scanErr := rows.Scan(&k); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		kinds = append(kinds, k)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate events: %v", err)
	}
	if len(kinds) == 0 || kinds[len(kinds)-1] != "shipped" {
		t.Errorf("history ends with %v, want a 'shipped' entry", kinds)
	}
}

func TestShippingIsRefusedForAnOrderThatWasNeverPicked(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, orderID, variantID := admintest.PendingOrderHoldingStock(t, pool)

	var stockBefore int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, variantID).
		Scan(&stockBefore); err != nil {
		t.Fatalf("read stock before dispatch: %v", err)
	}

	err := s.Ship(ctx, number, orders.Dispatch{Carrier: "black_cat", Tracking: "TW999"}, actor)
	if !errors.Is(err, orders.ErrRefused) {
		t.Fatalf("shipping a pending order gave %v, want ErrRefused", err)
	}

	var shipments, lines, held, consumed, events, notices int
	var stockAfter int32
	if queryErr := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM order_shipments WHERE order_id = $1),
		       (SELECT count(*) FROM order_shipment_lines WHERE order_id = $1),
		       (SELECT count(*) FROM inventory_reservations WHERE order_id = $1 AND state = 'held'),
		       (SELECT count(*) FROM inventory_reservations WHERE order_id = $1 AND state = 'consumed'),
		       (SELECT count(*) FROM order_events WHERE order_id = $1 AND kind = 'shipped'),
		       (SELECT count(*) FROM outbox_messages
		        WHERE topic = 'order.shipped' AND payload->>'order_number' = $2),
		       (SELECT stock_quantity FROM product_variants WHERE id = $3)`,
		orderID, number, variantID).
		Scan(&shipments, &lines, &held, &consumed, &events, &notices, &stockAfter); queryErr != nil {
		t.Fatalf("read refused dispatch effects: %v", queryErr)
	}
	if shipments != 0 || lines != 0 {
		t.Errorf("refused dispatch left %d shipments and %d shipment lines, want none", shipments, lines)
	}
	if held != 1 || consumed != 0 {
		t.Errorf("refused dispatch left reservations held=%d consumed=%d, want 1/0", held, consumed)
	}
	if events != 0 || notices != 0 {
		t.Errorf("refused dispatch left %d shipped events and %d notices, want none", events, notices)
	}
	if stockAfter != stockBefore {
		t.Errorf("stock changed from %d to %d across a refused dispatch", stockBefore, stockAfter)
	}

	// Positive companion: the same order becomes shippable after it is funded
	// and enters picking. A fixture that can never ship would prove no boundary.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin funding: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ref := "cs_ship_" + number
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, ref); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 100000, NULL, NULL)`, ref); err != nil {
		t.Fatalf("capture payment: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("move order to picking: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit funding: %v", err)
	}
	if err := s.Ship(ctx, number,
		orders.Dispatch{Carrier: "black_cat", Tracking: "TW999"}, actor); err != nil {
		t.Fatalf("ship the same order after picking: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT fulfillment_status FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read shipped status: %v", err)
	}
	if status != "shipped" {
		t.Errorf("same order ended %q after its admitted dispatch, want shipped", status)
	}
}

func TestShipNeedsACarrierAndATracking(t *testing.T) {
	ctx := t.Context()
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number, _ := pickingOrderHoldingStock(t)

	tests := []struct{ name, carrier, tracking string }{
		{"no carrier", "", "TW1"},
		{"no tracking", "black_cat", ""},
		{"a spelling outside the closed set", "黑貓", "TW1"},
		{"the display name is not the code", "黑貓宅急便", "TW1"},
		{"both blank", "", ""},
		{"whitespace only", "   ", "\t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.Ship(ctx, number, orders.Dispatch{Carrier: tt.carrier, Tracking: tt.tracking}, uuid.NullUUID{}); !errors.Is(err, orders.ErrInvalid) {
				t.Errorf("Ship(%q, %q) gave %v, want ErrInvalid", tt.carrier, tt.tracking, err)
			}
		})
	}
}

func TestAdvanceCannotShip(t *testing.T) {
	ctx := t.Context()
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number, orderID := pickingOrderHoldingStock(t)

	if _, err := s.Advance(ctx, number, "shipped", uuid.NullUUID{}); !errors.Is(err, orders.ErrRefused) {
		t.Fatalf("Advance to shipped gave %v, want ErrRefused", err)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT fulfillment_status FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "picking" {
		t.Errorf("order is %q, want picking — Advance moved it", status)
	}
}

func TestAdvanceRecordsWhoAndWhen(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number, orderID, _ := admintest.PendingOrderHoldingStock(t, pool)

	var staff uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name) VALUES ($1, 'admin', '出貨人員')
		RETURNING id`, "ship-"+number+"@goen.invalid").Scan(&staff); err != nil {
		t.Fatalf("create staff: %v", err)
	}

	if _, err := s.Advance(ctx, number, "cancelled", uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
		t.Fatalf("advance: %v", err)
	}

	var kind string
	var actor uuid.NullUUID
	if err := pool.QueryRow(ctx, `
		SELECT kind, actor_user_id FROM order_events
		WHERE order_id = $1 ORDER BY occurred_at DESC, id DESC LIMIT 1`,
		orderID).Scan(&kind, &actor); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if kind != "cancelled" {
		t.Errorf("latest event is %q, want cancelled", kind)
	}
	if !actor.Valid || actor.UUID != staff {
		t.Errorf("event actor is %v, want the staff member who acted", actor)
	}
}

func TestCancellingAnOrderInTheBackOfficeReturnsItsStock(t *testing.T) {
	ctx := t.Context()
	s := admintest.OrderStore(admintest.AdminRolePool(t, pool), admintest.Refunder{}, nil, nil)

	var vid uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' AND pv.is_active
		  AND pv.stock_quantity > pv.safety_stock + 1
		ORDER BY pv.id LIMIT 1`).Scan(&vid); err != nil {
		t.Fatalf("find a sellable variant: %v", err)
	}
	number := admintest.PlaceHeldOrder(t, pool, vid)

	var held int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&held); err != nil {
		t.Fatalf("read stock: %v", err)
	}

	subscriptions := admintest.SubscribeAtSafetyStock(t, pool, vid)
	var staff uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name) VALUES ($1, 'admin', '取消人員')
		RETURNING id`, "cancel-"+number+"@goen.invalid").Scan(&staff); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	// record_audit_event reads the actor from the CONTEXT, not from the parameter.
	staffCtx := user.NewContext(ctx, user.User{ID: staff.String(), Role: user.RoleAdmin})
	if _, err := s.Advance(staffCtx, number, "cancelled", uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	admintest.AssertRestockQueued(t, pool, subscriptions)
	var after int32
	var state string
	if err := pool.QueryRow(ctx, `
		SELECT pv.stock_quantity,
		       (SELECT r.state FROM inventory_reservations r
		        JOIN orders o ON o.id = r.order_id
		        WHERE o.order_number = $2 LIMIT 1)
		FROM product_variants pv WHERE pv.id = $1`, vid, number).Scan(&after, &state); err != nil {
		t.Fatalf("read stock after: %v", err)
	}
	if after != held+1 {
		t.Errorf("stock is %d after cancelling, want %d — the hold was not released", after, held+1)
	}
	if state != "released" {
		t.Errorf("the hold is %s, want released", state)
	}
}

// TestBackOfficeCancellationReadsHoldsAfterWinningTheOrderLock is the admin
// counterpart of the customer cancellation race. The expiry release queues
// first, changes the reservation while retaining the order lock, then commits;
// Advance must take its held snapshot only after that commit and still finish.
func TestBackOfficeCancellationReadsHoldsAfterWinningTheOrderLock(t *testing.T) {
	ctx := t.Context()
	var variantID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' AND pv.is_active
		  AND pv.stock_quantity > pv.safety_stock + 1
		ORDER BY pv.id LIMIT 1`).Scan(&variantID); err != nil {
		t.Fatalf("find a sellable variant: %v", err)
	}
	number := admintest.PlaceHeldOrder(t, pool, variantID)
	var orderID, reservationID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT o.id, r.id FROM orders o
		JOIN inventory_reservations r ON r.order_id = o.id
		WHERE o.order_number = $1 AND r.state = 'held'`, number).
		Scan(&orderID, &reservationID); err != nil {
		t.Fatalf("read order and held reservation: %v", err)
	}

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin order blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	var blockerPID int32
	if lockErr := blocker.QueryRow(ctx, `
		SELECT pg_backend_pid() FROM orders WHERE id = $1 FOR UPDATE`, orderID).
		Scan(&blockerPID); lockErr != nil {
		t.Fatalf("lock order: %v", lockErr)
	}

	sweepPool := admintest.NamedPool(t, pool, "admin-sweep-before-cancel")
	sweepTx, err := sweepPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin sweep release: %v", err)
	}
	defer func() { _ = sweepTx.Rollback(context.WithoutCancel(ctx)) }()
	sweepDone := make(chan error, 1)
	go func() {
		_, releaseErr := sweepTx.Exec(ctx, `SELECT release_reservation($1)`, reservationID)
		sweepDone <- releaseErr
	}()
	sweepPID := admintest.WaitForBlockedApplication(t, pool, ctx, "admin-sweep-before-cancel", blockerPID)

	var staff uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name) VALUES ($1, 'admin', '併發取消人員')
		RETURNING id`, "cancel-race-"+uuid.NewString()+"@goen.invalid").Scan(&staff); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	staffCtx := user.NewContext(ctx, user.User{ID: staff.String(), Role: user.RoleAdmin})
	cancelPool := admintest.NamedPool(t, pool, "admin-cancel-behind-sweep")
	cancelDone := make(chan error, 1)
	go func() {
		_, advanceErr := admintest.OrderStore(cancelPool, admintest.Refunder{}, nil, nil).Advance(
			staffCtx, number, "cancelled", uuid.NullUUID{UUID: staff, Valid: true})
		cancelDone <- advanceErr
	}()
	// PostgreSQL reports the earlier queued waiter as a soft blocker. Waiting
	// behind that PID also pins which operation will win when blocker commits.
	admintest.WaitForBlockedApplication(t, pool, ctx, "admin-cancel-behind-sweep", sweepPID)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release order blocker: %v", err)
	}
	if err := <-sweepDone; err != nil {
		t.Fatalf("sweeper release after order unlock: %v", err)
	}
	select {
	case err := <-cancelDone:
		t.Fatalf("admin cancellation passed an uncommitted sweep release: %v", err)
	default:
	}
	if err := sweepTx.Commit(ctx); err != nil {
		t.Fatalf("commit sweep release: %v", err)
	}
	if err := <-cancelDone; err != nil {
		t.Fatalf("admin cancel after sweep won the lock: %v", err)
	}

	var status, state string
	if err := pool.QueryRow(ctx, `
		SELECT o.fulfillment_status, r.state
		FROM orders o JOIN inventory_reservations r ON r.order_id = o.id
		WHERE o.id = $1`, orderID).Scan(&status, &state); err != nil {
		t.Fatalf("read final state: %v", err)
	}
	if status != "cancelled" || state != "released" {
		t.Errorf("final state is order=%s reservation=%s, want cancelled/released", status, state)
	}
}

func TestShippingEnqueuesTheDispatchNotice(t *testing.T) {
	ctx := t.Context()
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number := shippableOrder(t, "en")

	var staff uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name) VALUES ($1, 'admin', '出貨')
		RETURNING id`, "dispatch-"+number+"@goen.invalid").Scan(&staff); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	staffCtx := user.NewContext(ctx, user.User{ID: staff.String(), Role: user.RoleAdmin})

	if err := s.Ship(staffCtx, number, orders.Dispatch{Carrier: "black_cat", Tracking: "903-2214-0001"},
		uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
		t.Fatalf("ship: %v", err)
	}

	// Found by what the notice IS about, not by the dedupe key: a test bound to
	// the key asserts the deduplication scheme rather than the notice.
	var payload []byte
	if err := pool.QueryRow(ctx,
		`SELECT payload FROM outbox_messages
		 WHERE topic = 'order.shipped' AND payload->>'tracking' = $1`,
		"903-2214-0001").Scan(&payload); err != nil {
		t.Fatalf("no dispatch notice was enqueued for %s: %v", number, err)
	}
	var got email.OrderShipped
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode notice: %v", err)
	}
	want := email.OrderShipped{
		OrderNumber: number, Email: "ship@example.com", Name: "收件人",
		Carrier: "black_cat", Tracking: "903-2214-0001",
		Locale: "en",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("dispatch notice (-want +got):\n%s", diff)
	}
}

func shippableOrder(t *testing.T, locale string) string {
	t.Helper()
	return shippableOrderFor(t, locale, false)
}

// shippableOrderFor is shippableOrder for a home delivery, or, when pickup, for
// a store pickup chosen before the order settles, as checkout places one.
func shippableOrderFor(t *testing.T, locale string, pickup bool) string {
	t.Helper()
	ctx := t.Context()

	var variantID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE pv.is_active AND p.status = 'active' AND pv.stock_quantity - pv.safety_stock > 2
		LIMIT 1`).Scan(&variantID); err != nil {
		t.Fatalf("find variant: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents, locale)
		SELECT next_order_number(), v.id, sm.code, v.name, 0, $1
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE NOT $2 OR sm.destination_kind = 'pickup_point'
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, locale, pickup).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		SELECT $1, pv.id, pv.sku, p.name, 100000, 1 FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $2`, orderID, variantID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, interval '30 minutes', $3)`,
		orderID, variantID, "ship-fixture:"+number); err != nil {
		t.Fatalf("hold: %v", err)
	}
	private := `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'ship@example.com', '收件人', '0912345678', '110', '台北市', '信義區', '路 1 號')`
	if pickup {
		private = `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                pickup_chain, pickup_store_code, pickup_store_name)
		VALUES ($1, 'ship@example.com', '收件人', '0912345678',
		        'family_mart', '012345', '台北車站門市')`
	}
	if _, err := tx.Exec(ctx, private, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 100000)`,
		orderID, "cs_ship_"+number); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 100000, NULL, NULL)`,
		"cs_ship_"+number); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("move to picking: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number
}

func TestADeliveryAddressCanBeCorrectedUntilItShips(t *testing.T) {
	ctx, staffID := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number := shippableOrder(t, "zh-Hant")

	correction := &orders.DeliveryCorrection{
		Email: "fixed@example.com", Recipient: "收件人", Phone: "0922333444",
		PostalCode: "106", City: "台北市", District: "大安區", Street: "正確的地址 99 號",
	}
	if err := s.CorrectDelivery(ctx, number, correction); err != nil {
		t.Fatalf("correct a picking order: %v", err)
	}
	if got := streetOf(t, number); got != "正確的地址 99 號" {
		t.Errorf("the address is %q after the correction", got)
	}

	if err := s.Ship(ctx, number, orders.Dispatch{Carrier: "black_cat", Tracking: "903-2214-9999"},
		uuid.NullUUID{UUID: staffID, Valid: true}); err != nil {
		t.Fatalf("ship: %v", err)
	}
	correction.Street = "出貨後偷改的地址"
	if err := s.CorrectDelivery(ctx, number, correction); !errors.Is(err, orders.ErrTooLateToCorrect) {
		t.Fatalf("a shipped order was corrected: %v", err)
	}
	if got := streetOf(t, number); got != "正確的地址 99 號" {
		t.Errorf("the address changed after dispatch: %q", got)
	}
}

func TestCorrectingADeliveryDoesNotWriteTheAddressIntoTheAuditTrail(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number := shippableOrder(t, "zh-Hant")

	const street = "非常獨特的街道名稱 12345"
	if err := s.CorrectDelivery(ctx, number, &orders.DeliveryCorrection{
		Email: "audit@example.com", Recipient: "收件人", Phone: "0922333444",
		PostalCode: "106", City: "台北市", District: "大安區", Street: street,
	}); err != nil {
		t.Fatalf("correct: %v", err)
	}

	var after string
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(after::text, '') FROM audit_events
		WHERE action = 'order.delivery' ORDER BY occurred_at DESC LIMIT 1`).Scan(&after); err != nil {
		t.Fatalf("read the audit row: %v", err)
	}
	if after == "" {
		t.Fatal("no audit row was written for the correction")
	}
	if strings.Contains(after, street) {
		t.Errorf("the customer's address is in the audit trail: %s", after)
	}
	if !strings.Contains(after, number) {
		t.Errorf("the audit row does not say which order changed: %s", after)
	}
}

func TestCorrectingAPickupOrderCannotTurnItIntoAnAddressOne(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number := pickupOrderForCorrection(t)

	if err := s.CorrectDelivery(ctx, number, &orders.DeliveryCorrection{
		Email: "pickup@example.com", Recipient: "收件人", Phone: "0922333444",
		PostalCode: "106", City: "台北市", District: "大安區", Street: "不該存下來的地址",
		PickupChain: "hi_life", PickupStoreCode: "778899", PickupStoreName: "民生門市",
	}); err != nil {
		t.Fatalf("correct a pickup order: %v", err)
	}

	var street, brand, code *string
	if err := pool.QueryRow(ctx, `
		SELECT pd.street, pd.pickup_chain, pd.pickup_store_code
		FROM order_private_data pd JOIN orders o ON o.id = pd.order_id
		WHERE o.order_number = $1`, number).Scan(&street, &brand, &code); err != nil {
		t.Fatalf("read the delivery: %v", err)
	}
	if street != nil {
		t.Errorf("a pickup order kept a street address: %q", *street)
	}
	if brand == nil || *brand != "hi_life" || code == nil || *code != "778899" {
		t.Errorf("the pickup point was not updated: %v/%v", brand, code)
	}
}

func streetOf(t *testing.T, number string) string {
	t.Helper()
	var street string
	if err := pool.QueryRow(t.Context(), `
		SELECT coalesce(pd.street, '') FROM order_private_data pd
		JOIN orders o ON o.id = pd.order_id WHERE o.order_number = $1`,
		number).Scan(&street); err != nil {
		t.Fatalf("read street: %v", err)
	}
	return street
}

func pickupOrderForCorrection(t *testing.T) string {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE sm.destination_kind = 'pickup_point'
		ORDER BY v.effective_at DESC LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create pickup order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'FIX-SKU', '測試商品', 100000, 1)`, orderID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                pickup_chain, pickup_store_code, pickup_store_name)
		VALUES ($1, 'pickup@example.com', '收件', '0912345678',
		        'family_mart', '012345', '台北車站門市')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number
}

func TestTheBackOfficeCanFindAnOrder(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number, recipient, addr := searchableOrder(t)

	for _, term := range []string{
		number,
		strings.ToLower(number),
		// Sliced by RUNE: recipient[:2] cuts a character in half and PostgreSQL refuses invalid UTF-8.
		string([]rune(recipient)[:2]),
		string([]rune(addr)[:8]),
		strings.ToUpper(string([]rune(addr)[:8])),
	} {
		view, err := s.List(ctx, "", term)
		if err != nil {
			t.Fatalf("Orders(%q): %v", term, err)
		}
		if !hasOrder(view, number) {
			t.Errorf("searching %q did not find %s", term, number)
		}
		if view.Term != term {
			t.Errorf("the box lost the term: %q", view.Term)
		}
	}
}

func TestASearchIgnoresTheStatusFilter(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number, _, _ := searchableOrder(t)

	view, err := s.List(ctx, "shipped", number)
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	if !hasOrder(view, number) {
		t.Error("a search was filtered away by the status tab")
	}
}

func TestATooShortSearchIsNotASearch(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)

	view, err := s.List(ctx, "", "王")
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	if view.Searching() {
		t.Error("a one-character term reads as a search")
	}
}

func TestAnErasedOrderIsNotFoundByItsOldAddress(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number, recipient, addr := searchableOrder(t)

	if _, err := pool.Exec(ctx, `
		UPDATE order_private_data pd SET
			email = NULL, recipient_name = NULL, phone = NULL, postal_code = NULL,
			city = NULL, district = NULL, street = NULL, erased_at = now()
		FROM orders o WHERE pd.order_id = o.id AND o.order_number = $1`,
		number); err != nil {
		t.Fatalf("erase: %v", err)
	}

	for _, term := range []string{string([]rune(recipient)[:2]), string([]rune(addr)[:8])} {
		view, err := s.List(ctx, "", term)
		if err != nil {
			t.Fatalf("Orders(%q): %v", term, err)
		}
		if hasOrder(view, number) {
			t.Errorf("an erased order was found by %q, which it no longer holds", term)
		}
	}
	view, err := s.List(ctx, "", number)
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	if !hasOrder(view, number) {
		t.Error("an erased order cannot be found by its own number; it is still an order")
	}
}

// TestAnOrderSearchTakesWildcardsLiterally: "%%" passes the two-rune floor, and
// a typed _ is part of an address, not a stand-in for any character.
func TestAnOrderSearchTakesWildcardsLiterally(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	underscored, _, _ := searchableOrder(t)
	lettered, _, _ := searchableOrder(t)
	stem := strings.ReplaceAll(uuid.NewString(), "-", "")
	for number, addr := range map[string]string{
		underscored: "lk_" + stem + "@goen.invalid",
		lettered:    "lkx" + stem + "@goen.invalid",
	} {
		if _, err := pool.Exec(ctx, `
			UPDATE order_private_data pd SET email = $2
			FROM orders o WHERE pd.order_id = o.id AND o.order_number = $1`, number, addr); err != nil {
			t.Fatalf("address order %s: %v", number, err)
		}
	}

	view, err := s.List(ctx, "", "%%")
	if err != nil {
		t.Fatalf("Orders(%%%%): %v", err)
	}
	if len(view.Orders) != 0 {
		t.Errorf(`searching "%%%%" listed %d orders; no address or name starts with it`, len(view.Orders))
	}

	view, err = s.List(ctx, "", "lk_"+stem)
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	if !hasOrder(view, underscored) {
		t.Errorf("searching the underscored address did not find %s", underscored)
	}
	if hasOrder(view, lettered) {
		t.Errorf("a typed _ matched %s, whose address has an x there", lettered)
	}
}

func hasOrder(v admin.OrdersView, number string) bool {
	for i := range v.Orders {
		if v.Orders[i].Number == number {
			return true
		}
	}
	return false
}

func searchableOrder(t *testing.T) (number, recipient, addr string) {
	t.Helper()
	ctx := t.Context()
	recipient = "尋" + uuid.NewString()[:6]
	addr = "search-" + strings.ReplaceAll(uuid.NewString(), "-", "") + "@goen.invalid"

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'SEARCH-SKU', '測試商品', 100000, 1)`, orderID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, $2, $3, '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID, addr, recipient); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, recipient, addr
}

// TestTheBackOfficeSeesTheSystemCancelAtThePaymentDeadline: the sweeper's
// cancellation is the system's, in both languages, and never the customer's.
func TestTheBackOfficeSeesTheSystemCancelAtThePaymentDeadline(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number := admintest.PlaceUnpaidOrderHolding(t, pool, true)
	if _, err := pool.Exec(ctx, `
		UPDATE inventory_reservations
		SET created_at = now() - interval '2 hours', expires_at = now() - interval '1 minute'
		WHERE order_id = (SELECT id FROM orders WHERE order_number = $1)`, number); err != nil {
		t.Fatalf("expire the hold: %v", err)
	}
	if _, _, err := cart.NewStore(pool).Sweep(ctx, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	var found bool
	for _, e := range view.Timeline {
		if e.Label != i18n.KeyStatusCancelled {
			continue
		}
		found = true
		for locale, want := range map[i18n.Locale]string{i18n.ZhHant: "系統", i18n.En: "System"} {
			if got := e.By(i18n.WithLocale(ctx, locale)); got != want {
				t.Errorf("%s: the back office says %q cancelled it, want %q", locale, got, want)
			}
		}
	}
	if !found {
		t.Fatal("the sweep left no cancellation in the order's history")
	}
}

func TestTheBackOfficeSeesWhoCancelled(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	basket := cart.NewStore(pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number := admintest.PlaceUnpaidOrder(t, pool)

	if _, err := basket.CancelOrder(ctx, number); err != nil {
		t.Fatalf("the customer cancels: %v", err)
	}

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	var found bool
	for _, e := range view.Timeline {
		if e.Label != i18n.KeyStatusCancelled {
			continue
		}
		found = true
		if e.By(ctx) != "顧客" {
			t.Errorf("the back office says %q cancelled it, want 顧客", e.By(ctx))
		}
		if e.Note != "" {
			t.Errorf("the cancellation still stores words: %q", e.Note)
		}
	}
	if !found {
		t.Fatal("no cancellation in the order's history")
	}
}

func TestADeliveredOrderMarksItsParcelsDelivered(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number := shippableOrder(t, "zh-Hant")

	if err := s.Ship(ctx, number, orders.Dispatch{Carrier: "black_cat", Tracking: "DELIVERED-" + number}, actor); err != nil {
		t.Fatalf("Ship: %v", err)
	}
	before, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order after shipping: %v", err)
	}
	if len(before.Shipments) != 1 {
		t.Fatalf("%d parcels after shipping, want 1", len(before.Shipments))
	}
	if before.Shipments[0].Delivered() {
		t.Error("a parcel that has just left reads as delivered")
	}

	if _, advErr := s.Advance(ctx, number, "delivered", actor); advErr != nil {
		t.Fatalf("Advance to delivered: %v", advErr)
	}
	after, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order after delivery: %v", err)
	}
	if !after.Shipments[0].Delivered() {
		t.Error("the order is delivered and its parcel still says otherwise")
	}

	first := deliveredAt(t, number)
	if first.IsZero() {
		t.Fatal("the parcel has no delivery timestamp at all")
	}
	if _, err := s.Advance(ctx, number, "completed", actor); err != nil {
		t.Fatalf("Advance to completed: %v", err)
	}
	if again := deliveredAt(t, number); !again.Equal(first) {
		t.Errorf("the delivery timestamp moved from %s to %s",
			first.Format(time.RFC3339Nano), again.Format(time.RFC3339Nano))
	}
}

func TestAnOrderCompletedWithoutADeliveryStepStillStampsItsParcels(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number := shippableOrder(t, "zh-Hant")

	if err := s.Ship(ctx, number, orders.Dispatch{Carrier: "black_cat", Tracking: "COLLECTED-" + number}, actor); err != nil {
		t.Fatalf("Ship: %v", err)
	}
	if !deliveredAt(t, number).IsZero() {
		t.Fatal("a parcel that has just left is already stamped delivered; the fixture is wrong")
	}

	if _, err := s.Advance(ctx, number, "completed", actor); err != nil {
		t.Fatalf("Advance to completed: %v", err)
	}

	if deliveredAt(t, number).IsZero() {
		t.Error("an order completed without a delivery step left its parcel unstamped — " +
			"the customer's page says it never arrived and /admin/returns reads the " +
			"rescission window as never having started")
	}
}

func TestShippingIsRefusedWhenTheOrderHoldsNoStock(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number, orderID := pickingOrderHoldingStock(t)

	if _, err := pool.Exec(ctx, `
		UPDATE inventory_reservations SET state = 'released', settled_at = now()
		WHERE order_id = $1 AND state = 'held'`, orderID); err != nil {
		t.Fatalf("strand the order: %v", err)
	}

	err := s.Ship(ctx, number, orders.Dispatch{Carrier: "black_cat", Tracking: "NOHOLD-" + number}, uuid.NullUUID{})
	if !errors.Is(err, orders.ErrRefused) {
		t.Fatalf("shipping an order holding no stock = %v, want ErrRefused", err)
	}

	var shipments int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_shipments WHERE order_id = $1`, orderID).Scan(&shipments); err != nil {
		t.Fatalf("count shipments: %v", err)
	}
	if shipments != 0 {
		t.Errorf("%d parcels recorded against a refused dispatch, want 0", shipments)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT fulfillment_status FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "picking" {
		t.Errorf("order is %q after a refused dispatch, want picking", status)
	}
}

func deliveredAt(t *testing.T, number string) time.Time {
	t.Helper()
	var at *time.Time
	if err := pool.QueryRow(t.Context(), `
		SELECT sh.delivered_at FROM order_shipments sh
		JOIN orders o ON o.id = sh.order_id
		WHERE o.order_number = $1`, number).Scan(&at); err != nil {
		t.Fatalf("read the parcel: %v", err)
	}
	if at == nil {
		return time.Time{}
	}
	return *at
}

func TestTheOrderPageShowsTheInvoiceChoice(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number := admintest.PlaceUnpaidOrder(t, pool)

	var orderID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM orders WHERE order_number = $1`, number).Scan(&orderID); err != nil {
		t.Fatalf("read the order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_preferences
		    (order_id, invoice_type, tax_id, customer_name, customer_email)
		VALUES ($1, 'company', '04595252', '測試股份有限公司',
		        'admin-invoice@goen.invalid')`, orderID); err != nil {
		t.Fatalf("record the preference: %v", err)
	}

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if !view.HasInvoice() {
		t.Fatal("the order page does not show a 發票 preference that exists")
	}
	if view.InvoiceText(ctx) != "公司統編 04595252" {
		t.Errorf("the page says %q, want 公司統編 04595252", view.InvoiceText(ctx))
	}

	plain, err := s.Order(ctx, admintest.PlaceUnpaidOrder(t, pool))
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if plain.HasInvoice() {
		t.Error("an order with no preference reports one")
	}
}

func heldFor(t *testing.T, orderID, variantID uuid.UUID) int32 {
	t.Helper()
	var n int32
	if err := pool.QueryRow(t.Context(), `
		SELECT coalesce(sum(quantity), 0)::integer FROM inventory_reservations
		WHERE order_id = $1 AND variant_id = $2 AND state = 'held'`,
		orderID, variantID).Scan(&n); err != nil {
		t.Fatalf("read held: %v", err)
	}
	return n
}

func shippedFor(t *testing.T, lineID uuid.UUID) int32 {
	t.Helper()
	var n int32
	if err := pool.QueryRow(t.Context(), `
		SELECT coalesce(sum(quantity), 0)::integer FROM order_shipment_lines
		WHERE order_line_id = $1`, lineID).Scan(&n); err != nil {
		t.Fatalf("read shipped: %v", err)
	}
	return n
}

func TestAnOrderCanShipInTwoParcels(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, orderID, lines, variants := admintest.TwoLineOrderWithStock(t, pool, "partial")

	if err := s.Ship(ctx, number, orders.Dispatch{
		Carrier: "black_cat", Tracking: "P1-" + number,
		Lines: map[uuid.UUID]int32{lines[0]: 2},
	}, actor); err != nil {
		t.Fatalf("first parcel: %v", err)
	}

	if got := shippedFor(t, lines[0]); got != 2 {
		t.Errorf("line 1 has shipped %d, want 2", got)
	}
	if got := shippedFor(t, lines[1]); got != 0 {
		t.Errorf("line 2 has shipped %d, want 0 — it was not in this parcel", got)
	}
	if got := heldFor(t, orderID, variants[0]); got != 1 {
		t.Errorf("line 1 still holds %d, want 1", got)
	}
	if got := heldFor(t, orderID, variants[1]); got != 3 {
		t.Errorf("line 2 still holds %d, want 3 — nothing of it has gone out", got)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT fulfillment_status FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "shipped" {
		t.Errorf("the order is %q after its first parcel, want shipped", status)
	}

	if err := s.Ship(ctx, number, orders.Dispatch{
		Carrier: "black_cat", Tracking: "P2-" + number,
	}, actor); err != nil {
		t.Fatalf("second parcel: %v", err)
	}

	if got := shippedFor(t, lines[0]); got != 3 {
		t.Errorf("line 1 has shipped %d after both parcels, want 3", got)
	}
	if got := shippedFor(t, lines[1]); got != 3 {
		t.Errorf("line 2 has shipped %d after both parcels, want 3", got)
	}
	if got := heldFor(t, orderID, variants[0]); got != 0 {
		t.Errorf("line 1 still holds %d after shipping everything, want 0 — a "+
			"sweeper would return goods that have gone out", got)
	}
	if got := heldFor(t, orderID, variants[1]); got != 0 {
		t.Errorf("line 2 still holds %d after shipping everything, want 0", got)
	}

	var parcels int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_shipments WHERE order_id = $1`, orderID).Scan(&parcels); err != nil {
		t.Fatalf("count parcels: %v", err)
	}
	if parcels != 2 {
		t.Errorf("%d parcels, want 2", parcels)
	}

	err := s.Ship(ctx, number, orders.Dispatch{
		Carrier: "black_cat", Tracking: "P3-" + number,
	}, actor)
	if !errors.Is(err, orders.ErrRefused) {
		t.Errorf("a third parcel on a fully shipped order = %v, want ErrRefused", err)
	}
}

func TestAParcelCannotCarryMoreThanRemains(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, orderID, lines, variants := admintest.TwoLineOrderWithStock(t, pool, "overship")

	err := s.Ship(ctx, number, orders.Dispatch{
		Carrier: "black_cat", Tracking: "OVER-" + number,
		Lines: map[uuid.UUID]int32{lines[0]: 4},
	}, actor)
	if !errors.Is(err, orders.ErrQuantity) {
		t.Fatalf("shipping 4 of 3 = %v, want ErrQuantity", err)
	}

	var parcels int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_shipments WHERE order_id = $1`, orderID).Scan(&parcels); err != nil {
		t.Fatalf("count parcels: %v", err)
	}
	if parcels != 0 {
		t.Errorf("%d parcels after a refused dispatch, want 0", parcels)
	}
	if got := heldFor(t, orderID, variants[0]); got != 3 {
		t.Errorf("the hold moved to %d on a refused dispatch, want 3", got)
	}
}

func TestAnEmptyParcelIsRefused(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, orderID, lines, _ := admintest.TwoLineOrderWithStock(t, pool, "emptyparcel")

	err := s.Ship(ctx, number, orders.Dispatch{
		Carrier: "black_cat", Tracking: "EMPTY-" + number,
		Lines: map[uuid.UUID]int32{lines[0]: 0, lines[1]: 0},
	}, actor)
	if !errors.Is(err, orders.ErrQuantity) {
		t.Fatalf("a parcel carrying nothing = %v, want ErrQuantity", err)
	}
	var parcels int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_shipments WHERE order_id = $1`, orderID).Scan(&parcels); err != nil {
		t.Fatalf("count parcels: %v", err)
	}
	if parcels != 0 {
		t.Errorf("%d parcels after an empty dispatch, want 0", parcels)
	}
}

func TestEachParcelTellsTheCustomer(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, orderID, lines, _ := admintest.TwoLineOrderWithStock(t, pool, "notice")

	if err := s.Ship(ctx, number, orders.Dispatch{
		Carrier: "black_cat", Tracking: "N1-" + number,
		Lines: map[uuid.UUID]int32{lines[0]: 3},
	}, actor); err != nil {
		t.Fatalf("first parcel: %v", err)
	}
	if err := s.Ship(ctx, number, orders.Dispatch{
		Carrier: "black_cat", Tracking: "N2-" + number,
		Lines: map[uuid.UUID]int32{lines[1]: 3},
	}, actor); err != nil {
		t.Fatalf("second parcel: %v", err)
	}

	var notices int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE topic = 'order.shipped' AND payload->>'order_number' = $1`,
		number).Scan(&notices); err != nil {
		t.Fatalf("count notices: %v", err)
	}
	if notices != 2 {
		t.Errorf("%d dispatch notices for two parcels, want 2 — a customer told "+
			"about one box is left wondering about the other", notices)
	}

	var events int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_events WHERE order_id = $1 AND kind = 'shipped'`,
		orderID).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 2 {
		t.Errorf("%d shipped events for two parcels, want 2", events)
	}
}

// TestAnOrderCannotFinishWhileItStillOwesAParcel keeps stock from being
// stranded permanently and invisibly.
//
// An order ships in as many parcels as it takes, and only the first moves the
// status. Finishing one that still owes a parcel would leave the remaining
// reservations `held` with no door out: release_reservation refuses them by name
// because a completed order is committed, ExpiredReservations excludes committed
// orders by predicate, and /admin/health counts expired holds with that same
// predicate.
//
// It refuses rather than releasing: what has not gone out is either still going
// out — CanShip already allows the second parcel — or it is an abandonment,
// which is a decision a person makes rather than a side effect of a dropdown.
func TestAnOrderCannotFinishWhileItStillOwesAParcel(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, orderID, lines, variants := admintest.TwoLineOrderWithStock(t, pool, "unfinished")

	// One parcel, carrying part of the first line only.
	if err := s.Ship(ctx, number, orders.Dispatch{
		Carrier: "black_cat", Tracking: "U1-" + number,
		Lines: map[uuid.UUID]int32{lines[0]: 1},
	}, actor); err != nil {
		t.Fatalf("first parcel: %v", err)
	}

	// DELIVERED is allowed and must be: it is a fact about the parcel that went
	// out, and it is the ONLY thing that stamps order_shipments.delivered_at.
	// Refuse it and /admin/returns reads 尚未送達 for goods the customer holds,
	// on the screen built to inform a 消保法 §19 decision.
	if _, err := s.Advance(ctx, number, "delivered", actor); err != nil {
		t.Fatalf("a partially shipped order could not record its first parcel as "+
			"delivered: %v", err)
	}
	var stamped int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_shipments sh JOIN orders o ON o.id = sh.order_id
		WHERE o.order_number = $1 AND sh.delivered_at IS NOT NULL`,
		number).Scan(&stamped); err != nil {
		t.Fatalf("count stamped parcels: %v", err)
	}
	if stamped == 0 {
		t.Error("no parcel was stamped delivered, so the rescission window never starts")
	}

	// COMPLETED is refused: the order is not finished while it still owes a parcel.
	_, err := s.Advance(ctx, number, "completed", actor)
	if err == nil {
		t.Fatal("an order still owing a parcel was completed; whatever is still " +
			"held is now stranded with no door out")
	}
	// The `ok` is asserted, not used as a condition: wrapping that puts the
	// message in a string and the PgError nowhere in the chain would skip the
	// whole clause, leaving the test asking only that SOMETHING was refused.
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		t.Fatalf("the refusal does not carry the rule that made it: %v\n"+
			"Asserting only that an error happened cannot tell one rule from another", err)
	}
	if pgErr.ConstraintName != "orders_finished_when_shipped" {
		t.Errorf("refused by %q, want orders_finished_when_shipped — a statement "+
			"meant to prove one rule often trips another first", pgErr.ConstraintName)
	}

	// And once everything has gone out, finishing works and nothing is held.
	if err := s.Ship(ctx, number, orders.Dispatch{
		Carrier: "black_cat", Tracking: "U2-" + number,
	}, actor); err != nil {
		t.Fatalf("second parcel: %v", err)
	}
	if _, err := s.Advance(ctx, number, "completed", actor); err != nil {
		t.Fatalf("a fully shipped order could not be completed: %v", err)
	}
	for i, v := range variants {
		if got := heldFor(t, orderID, v); got != 0 {
			t.Errorf("line %d still holds %d on a completed order", i+1, got)
		}
	}
}

// TestTwoCarriersSharingATrackingNumberBothNotify holds the dispatch notice's
// dedupe key to the fact it deduplicates.
//
// order_shipments is unique on (carrier, tracking_number) — the schema's own
// statement that a tracking number identifies a parcel only alongside who is
// carrying it. Keyed on the tracking number ALONE, a second shipment with a
// colliding number from a different carrier meets ON CONFLICT DO NOTHING in the
// outbox: Ship succeeds, the parcel goes out, and the customer is never told.
// Two parcels of one ORDER carry two tracking numbers; two parcels of different
// orders may share one.
func TestTwoCarriersSharingATrackingNumberBothNotify(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}

	const shared = "SHARED-1234567890"
	first, _, firstLines, _ := admintest.TwoLineOrderWithStock(t, pool, "carrier-a")
	second, _, secondLines, _ := admintest.TwoLineOrderWithStock(t, pool, "carrier-b")

	if err := s.Ship(ctx, first, orders.Dispatch{
		Carrier: "black_cat", Tracking: shared,
		Lines: map[uuid.UUID]int32{firstLines[0]: 1},
	}, actor); err != nil {
		t.Fatalf("first carrier: %v", err)
	}
	if err := s.Ship(ctx, second, orders.Dispatch{
		Carrier: "hct", Tracking: shared,
		Lines: map[uuid.UUID]int32{secondLines[0]: 1},
	}, actor); err != nil {
		t.Fatalf("second carrier: %v — the database allows the pair, so the "+
			"application must too", err)
	}

	var notices int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE topic = 'order.shipped' AND payload->>'tracking' = $1`, shared).Scan(&notices); err != nil {
		t.Fatalf("count dispatch notices: %v", err)
	}
	if notices != 2 {
		t.Errorf("%d dispatch notices for two parcels sharing a tracking number, "+
			"want 2 — one customer's parcel left and nothing told them", notices)
	}
}

// TestADeliveredOrderCanStillShipWhatItOwes is the exit from a trap that would
// otherwise have none. orders_legal_transition permits shipped -> delivered with
// a line still outstanding, deliberately — delivered says the parcels that WENT
// OUT have arrived — and orders_finished_when_shipped then refuses 'completed'.
// Without a dispatch form at 'delivered' the order is wedged for ever and the
// outstanding line's hold is stranded: release_reservation refuses a committed
// order, ExpiredReservations excludes it, and /admin/health counts neither.
func TestADeliveredOrderCanStillShipWhatItOwes(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	number, _, lines, _ := admintest.TwoLineOrderWithStock(t, pool, "wedged")

	// One parcel carrying part of the first line, then straight to delivered:
	// what went out has arrived, and the rest is still to come.
	if err := s.Ship(ctx, number, orders.Dispatch{
		Carrier: "black_cat", Tracking: "D1-" + number,
		Lines: map[uuid.UUID]int32{lines[0]: 1},
	}, actor); err != nil {
		t.Fatalf("first parcel: %v", err)
	}
	if _, err := s.Advance(ctx, number, "delivered", actor); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("read the order: %v", err)
	}
	if !view.CanShip {
		t.Fatal("a delivered order that still owes a parcel offers no dispatch form, " +
			"and completing it is refused — the order is wedged and its remaining " +
			"hold is stranded off the shelf where nothing counts it")
	}

	if err := s.Ship(ctx, number, orders.Dispatch{
		Carrier: "black_cat", Tracking: "D2-" + number,
	}, actor); err != nil {
		t.Fatalf("the second parcel of a delivered order was refused: %v", err)
	}
	if _, err := s.Advance(ctx, number, "completed", actor); err != nil {
		t.Errorf("finishing an order that owes nothing was refused: %v", err)
	}

	var held int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM inventory_reservations ir
		JOIN orders o ON o.id = ir.order_id
		WHERE o.order_number = $1 AND ir.state = 'held'`, number).Scan(&held); err != nil {
		t.Fatalf("read the holds: %v", err)
	}
	if held != 0 {
		t.Errorf("%d hold(s) still on the shelf after everything shipped", held)
	}
}

func TestAPickupOrderDispatchNoticeIsMarkedAsPickup(t *testing.T) {
	ctx := t.Context()
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number := pickupOrderForCorrection(t)
	var orderID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM orders WHERE order_number = $1`, number).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if err := s.EnqueueShippedNotice(ctx, orderID, "綠界", "PICKUP-"+number); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	var pickup bool
	if err := pool.QueryRow(ctx, `
		SELECT (payload->>'pickup')::boolean FROM outbox_messages
		WHERE topic = 'order.shipped' AND payload->>'tracking' = $1`, "PICKUP-"+number).Scan(&pickup); err != nil {
		t.Fatalf("read notice: %v", err)
	}
	if !pickup {
		t.Error("a convenience-store order's dispatch notice is not marked as pickup, so it reads as a home delivery")
	}
}

type fixedInvoices []invoice.Document

func (f fixedInvoices) Documents(context.Context, string) ([]invoice.Document, error) { return f, nil }

func TestTheOrderPageShowsAnInvoiceAtTheTimeTheProviderIssuedIt(t *testing.T) {
	ctx := t.Context()
	number := admintest.PlaceUnpaidOrder(t, pool)
	issued := time.Date(2026, 9, 30, 9, 30, 0, 0, time.UTC) // ECPay said "2026-09-30 17:30:00" Taipei time
	s := admintest.OrderStore(pool, admintest.Refunder{}, fixedInvoices{{Kind: "invoice", Number: "AB12345678", IssuedAt: issued}}, admintest.DisabledInvoiceWriter{})

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if len(view.InvoiceDocuments) != 1 || view.InvoiceDocuments[0].IssuedAt != "2026-09-30 17:30" {
		t.Errorf("invoice documents = %+v, want one issued at 2026-09-30 17:30", view.InvoiceDocuments)
	}
}

func TestOrderSearchLabelsAnUnpaidOrderAsAwaitingPayment(t *testing.T) {
	ctx := t.Context()
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number := admintest.PlaceUnpaidOrder(t, pool)

	view, err := s.List(ctx, "", number)
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	if len(view.Orders) != 1 || view.Orders[0].Number != number {
		t.Fatalf("searching %s found %+v, want that order only", number, view.Orders)
	}
	if got, want := view.Orders[0].StatusText, i18n.T(ctx, i18n.KeyAdminStatusPending); got != want {
		t.Errorf("a searched unpaid order reads %q, want %q", got, want)
	}
}

func TestAdvanceRefusesTheStatusAnOrderAlreadyHas(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, false)

	if _, err := s.Advance(ctx, number, order.FulfillmentPicking, actor); err != nil {
		t.Fatalf("first picking: %v", err)
	}
	if _, err := s.Advance(ctx, number, order.FulfillmentPicking, actor); !errors.Is(err, orders.ErrRefused) {
		t.Fatalf("a repeated picking gave %v, want ErrRefused", err)
	}
	var events, audits int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM order_events WHERE order_id = $1 AND kind = 'picking'),
		       (SELECT count(*) FROM audit_events WHERE action = 'order.advance' AND entity_id = $1)`,
		orderID).Scan(&events, &audits); err != nil {
		t.Fatalf("count: %v", err)
	}
	if events != 1 || audits != 1 {
		t.Errorf("picking recorded %d events and %d audit rows, want 1 and 1", events, audits)
	}

	pending := admintest.PlaceUnpaidOrder(t, pool)
	if _, err := s.Advance(ctx, pending, order.FulfillmentPending, actor); !errors.Is(err, orders.ErrRefused) {
		t.Errorf("pending on a pending order gave %v, want ErrRefused", err)
	}
}

func TestTheStatusMenuOffersOnlyWhatTheDatabaseWillAccept(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	offers := func(v *admin.OrderView, status order.FulfillmentStatus) bool {
		for _, n := range v.Next {
			if n.Value == status {
				return true
			}
		}
		return false
	}

	unpaid, err := s.Order(ctx, admintest.PlaceUnpaidOrder(t, pool))
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if offers(&unpaid, order.FulfillmentPicking) {
		t.Error("an unpaid order is offered picking, which orders_funded_to_leave_pending refuses")
	}
	if !unpaid.NextIsDestructive() {
		t.Error("an unpaid order's menu preselects cancelling")
	}

	number, _, lines, _ := admintest.TwoLineOrderWithStock(t, pool, "menu")
	if err = s.Ship(ctx, number, orders.Dispatch{
		Carrier: "black_cat", Tracking: "MENU-" + number, Lines: map[uuid.UUID]int32{lines[0]: 1},
	}, actor); err != nil {
		t.Fatalf("first parcel: %v", err)
	}
	partly, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if offers(&partly, order.FulfillmentCompleted) {
		t.Error("an order still owing a parcel is offered completed, which orders_finished_when_shipped refuses")
	}
	if !offers(&partly, order.FulfillmentDelivered) {
		t.Error("delivered must stay offered: it is the only way to record that the first parcel arrived")
	}
}

func TestARefusedStatusMoveNamesItsReason(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	h := admintest.OrderDesk(s)
	post := func(number, status string) string {
		t.Helper()
		form := url.Values{"status": {status}}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/orders/"+number+"/status", strings.NewReader(form.Encode()))
		req.SetPathValue("number", number)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		admintest.BackOffice.RequireStaff(h.Advance)(w, req)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("POST status=%s = %d, want 303", status, w.Code)
		}
		return w.Header().Get("Location")
	}

	unpaid := admintest.PlaceUnpaidOrder(t, pool)
	if got, want := post(unpaid, "picking"), "/admin/orders/"+unpaid+"?unfunded=1"; got != want {
		t.Errorf("picking an unpaid order redirected to %q, want %q", got, want)
	}

	number, _, lines, _ := admintest.TwoLineOrderWithStock(t, pool, "reason")
	if err := s.Ship(ctx, number, orders.Dispatch{
		Carrier: "black_cat", Tracking: "RSN-" + number, Lines: map[uuid.UUID]int32{lines[0]: 1},
	}, uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
		t.Fatalf("first parcel: %v", err)
	}
	if got, want := post(number, "completed"), "/admin/orders/"+number+"?owesparcel=1"; got != want {
		t.Errorf("completing an order that owes a parcel redirected to %q, want %q", got, want)
	}
}

func TestAnAuditEntryNamesItsOrderAndLinksIt(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number := shippableOrder(t, "zh-Hant")
	if err := s.Ship(ctx, number, orders.Dispatch{Carrier: "black_cat", Tracking: "AUD-" + number},
		uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
		t.Fatalf("Ship: %v", err)
	}

	view, err := audit.NewStore(pool).Events(ctx)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	for _, e := range view.Rows {
		if e.Action == "order.ship" && e.Subject == number {
			if e.Href != "/admin/orders/"+number {
				t.Errorf("the shipment entry links %q, want /admin/orders/%s", e.Href, number)
			}
			return
		}
	}
	t.Errorf("no order.ship entry names %s among %d rows", number, len(view.Rows))
}

func TestARefusedDispatchKeepsWhatWasTyped(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	h := admintest.OrderDesk(admintest.OrderStore(pool, admintest.Refunder{}, nil, nil))
	number, _, lines, _ := admintest.TwoLineOrderWithStock(t, pool, "retype")

	form := url.Values{
		"carrier": {"black_cat"}, "tracking": {"9001-2345"}, "qty_" + lines[0].String(): {"99"},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/orders/"+number+"/ship", strings.NewReader(form.Encode()))
	req.SetPathValue("number", number)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	admintest.BackOffice.RequireStaff(h.Ship)(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a quantity above what is outstanding answered %d, want 422", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{`value="black_cat"`, `value="9001-2345"`, `value="99"`, `id="ship-qty-error"`, `aria-invalid="true"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the refused dispatch is missing %q", want)
		}
	}
}

func TestTheDashboardAndTheQueueTabsSplitPendingTheSameWay(t *testing.T) {
	ctx := t.Context()
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	tab := func(v admin.OrdersView, status admin.QueueFilter) int64 {
		for _, tb := range v.Tabs {
			if tb.Value == status {
				return tb.Count
			}
		}
		t.Fatalf("no %q tab", status)
		return 0
	}
	before, err := s.List(ctx, "", "")
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	dashBefore, err := s.Dashboard(ctx)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}

	unpaid := admintest.PlaceUnpaidOrder(t, pool)
	funded, _, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, false)

	after, err := s.List(ctx, "", "")
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	dashAfter, err := s.Dashboard(ctx)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if got := tab(after, admin.QueueAwaitingPayment) - tab(before, admin.QueueAwaitingPayment); got != 1 {
		t.Errorf("the awaiting-payment tab grew by %d, want 1: a funded order is not awaiting payment", got)
	}
	if got := tab(after, admin.QueueReady) - tab(before, admin.QueueReady); got != 1 {
		t.Errorf("the ready tab grew by %d, want 1", got)
	}
	if got := dashAfter.PendingOrders - dashBefore.PendingOrders; got != 1 {
		t.Errorf("the awaiting-payment tile grew by %d, want 1", got)
	}
	if got := dashAfter.ReadyOrders - dashBefore.ReadyOrders; got != 1 {
		t.Errorf("the ready tile grew by %d, want 1", got)
	}

	for status, want := range map[admin.QueueFilter]struct{ in, out string }{
		admin.QueueAwaitingPayment: {in: unpaid, out: funded},
		admin.QueueReady:           {in: funded, out: unpaid},
	} {
		view, err := s.List(ctx, status, "")
		if err != nil {
			t.Fatalf("Orders(%s): %v", status, err)
		}
		var sawIn, sawOut bool
		for _, o := range view.Orders {
			sawIn = sawIn || o.Number == want.in
			sawOut = sawOut || o.Number == want.out
		}
		if !sawIn || sawOut {
			t.Errorf("tab %q lists the expected order = %t and the other = %t, want true and false", status, sawIn, sawOut)
		}
	}
}

func TestTheOrderPageSaysHowItWasPaidAndWhatWasRefunded(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, false)

	if _, err := pool.Exec(ctx,
		`UPDATE payments SET card_brand = 'visa', card_last4 = '4242' WHERE order_id = $1`, orderID); err != nil {
		t.Fatalf("record the card: %v", err)
	}
	var refundID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents, reason, status, provider_ref, succeeded_at)
		SELECT p.id, 'order-page:' || ($1::uuid)::text, 120000, '顧客改變心意', 'succeeded',
		       're_order_page_' || ($1::uuid)::text, now()
		FROM payments p WHERE p.order_id = $1 AND p.status = 'succeeded'
		RETURNING id`, orderID).Scan(&refundID); err != nil {
		t.Fatalf("record a refund: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT record_audit_event($1, 'refund.succeeded', 'refunds', $2)`,
		staff, refundID); err != nil {
		t.Fatalf("record who: %v", err)
	}

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if view.Payment.Card != "Visa •••• 4242" || view.Payment.Captured != pages.TWD(500000) || view.Payment.PaidAt == "" {
		t.Errorf("payment = %+v, want the card, the captured amount and a time", view.Payment)
	}
	if len(view.Refunds) != 1 {
		t.Fatalf("refunds = %+v, want one", view.Refunds)
	}
	r := view.Refunds[0]
	if r.Amount != pages.TWD(120000) || r.Reason != "顧客改變心意" || r.Staff == "" || r.At == "" {
		t.Errorf("refund = %+v, want its amount, time, reason and staff member", r)
	}
}

// TestTheBackOfficeSaysRefundedOnceEveryUnitIsInAnApprovedReturn holds that the order's header and the queue's row
// read the fact the shopper's pages do, and that a return of part of the order leaves the delivery word.
func TestTheBackOfficeSaysRefundedOnceEveryUnitIsInAnApprovedReturn(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)

	for _, tc := range []struct {
		name     string
		returned int32
		wantKey  i18n.Key
	}{
		{"one unit of two", 1, i18n.KeyAdminStatusShipped},
		{"both units", 2, i18n.KeyStatusRefunded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := i18n.T(ctx, tc.wantKey)
			returnID, number := admintest.ReturnedOrder(t, pool, tc.returned)
			if _, err := pool.Exec(ctx, `
				UPDATE return_requests
				SET status = 'approved', decided_at = now(), goods_refund_cents = 0, card_refund_cents = 0,
				    credit_refund_cents = 0
				WHERE id = $1`, returnID); err != nil {
				t.Fatalf("approve return: %v", err)
			}

			view, err := s.Order(ctx, number)
			if err != nil {
				t.Fatalf("Order: %v", err)
			}
			if view.StatusText != want {
				t.Errorf("Order(%s).StatusText = %q, want %q", tc.name, view.StatusText, want)
			}
			queue, err := s.List(ctx, admin.QueueAll, number)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(queue.Orders) != 1 || queue.Orders[0].StatusText != want {
				t.Errorf("the queue row for %s = %+v, want StatusText %q", tc.name, queue.Orders, want)
			}
		})
	}
}
