//go:build integration

package returns_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/returns"
	"github.com/koopa0/goen/internal/pgtx"
)

// TestTwoInspectionsOfOneReturnDoNotDeadlock: the inspection form's lines
// arrive in map order, so two submissions of one return can name its lines in
// opposite orders. Both wait on the order here; once it is free, the one that
// goes first inspects every line and the other is refused as already inspected,
// rather than each holding a line the other needs.
func TestTwoInspectionsOfOneReturnDoNotDeadlock(t *testing.T) {
	ctx, actor := admintest.StaffContext(t, pool)
	staff := uuid.NullUUID{UUID: actor, Valid: true}
	requestID, number, lines := approvedTwoLineReturn(t)

	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the order holder: %v", err)
	}
	defer pgtx.Rollback(ctx, holder)
	if _, err = holder.Exec(ctx, `SELECT 1 FROM orders WHERE order_number = $1 FOR UPDATE`, number); err != nil {
		t.Fatalf("hold order %s: %v", number, err)
	}

	orders := [2][]returns.LineInspection{
		{{OrderLineID: lines[0], Received: 1, Restocked: 1}, {OrderLineID: lines[1], Received: 1, Restocked: 1}},
		{{OrderLineID: lines[1], Received: 1, Restocked: 1}, {OrderLineID: lines[0], Received: 1, Restocked: 1}},
	}
	var done [2]chan error
	for i := range orders {
		name := "inspection_lock_" + uuid.NewString()[:8]
		s := storeOver(admintest.NamedPool(t, pool, name), admintest.Refunder{})
		done[i] = make(chan error, 1)
		go func() { done[i] <- s.Inspect(ctx, requestID.String(), orders[i], staff) }()
		waitForLock(t, name, done[i])
	}
	if err = holder.Commit(ctx); err != nil {
		t.Fatalf("release order %s: %v", number, err)
	}

	var inspected, refused int
	for i := range done {
		got := <-done[i]
		if pgErr, ok := errors.AsType[*pgconn.PgError](got); ok && pgErr.Code == "40P01" {
			t.Fatalf("inspection %d of return %s deadlocked: %v", i, requestID, got)
		}
		switch {
		case got == nil:
			inspected++
		case errors.Is(got, returns.ErrRefused):
			refused++
		default:
			t.Errorf("inspection %d of return %s = %v, want success or ErrRefused", i, requestID, got)
		}
	}
	if inspected != 1 || refused != 1 {
		t.Errorf("two inspections of return %s: %d inspected and %d refused, want one of each", requestID, inspected, refused)
	}
	var trail int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE entity_id = $1 AND action = $2`,
		requestID, string(audit.ActionInspectReturn)).Scan(&trail); err != nil {
		t.Fatalf("read the inspection trail: %v", err)
	}
	if trail != 1 {
		t.Errorf("return %s has %d inspection audit rows, want 1", requestID, trail)
	}
	keys := []string{
		"return:" + requestID.String() + ":" + lines[0].String(),
		"return:" + requestID.String() + ":" + lines[1].String(),
	}
	var movements int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM inventory_movements WHERE idempotency_key = ANY ($1)`,
		keys).Scan(&movements); err != nil {
		t.Fatalf("read the restock movements: %v", err)
	}
	if movements != len(keys) {
		t.Errorf("return %s posted %d restock movements, want one per line (%d)", requestID, movements, len(keys))
	}
}

// approvedTwoLineReturn is a shipped, paid order of two lines and an approved
// return of both.
func approvedTwoLineReturn(t *testing.T) (requestID uuid.UUID, number string, lines [2]uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer pgtx.Rollback(ctx, tx)

	var variantID uuid.UUID
	var sku, productName string
	if err = tx.QueryRow(ctx, `
		SELECT pv.id, pv.sku, p.name FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE pv.is_active AND p.status = 'active' LIMIT 1`).Scan(&variantID, &sku, &productName); err != nil {
		t.Fatalf("find variant: %v", err)
	}
	var orderID uuid.UUID
	if err = tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	for i := range lines {
		if err = tx.QueryRow(ctx, `
			INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity, position)
			VALUES ($1, $2, $3, $4, 100000, 1, $5) RETURNING id`,
			orderID, variantID, sku, productName, i).Scan(&lines[i]); err != nil {
			t.Fatalf("create line %d: %v", i, err)
		}
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'inspect@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	session := "cs_inspect_" + number
	if _, err = tx.Exec(ctx, `SELECT open_payment($1, $2, 200000)`, orderID, session); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err = tx.Exec(ctx, `SELECT capture_payment($1, 200000, NULL, NULL)`, session); err != nil {
		t.Fatalf("capture: %v", err)
	}
	admintest.MoveOrderToShipped(t, tx, orderID)
	var shipmentID uuid.UUID
	if err = tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, 'black_cat', 'T-'||$2) RETURNING id`, orderID, number).Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	if err = tx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason) VALUES ($1, '不合用') RETURNING id`,
		orderID).Scan(&requestID); err != nil {
		t.Fatalf("create return request: %v", err)
	}
	for _, line := range lines {
		if _, err = tx.Exec(ctx, `
			INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
			VALUES ($1, $2, $3, 1)`, orderID, shipmentID, line); err != nil {
			t.Fatalf("ship line: %v", err)
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
			VALUES ($1, $2, $3, 1)`, orderID, requestID, line); err != nil {
			t.Fatalf("return line: %v", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err = pool.Exec(ctx, `
		UPDATE return_requests SET status = 'approved', decided_at = now(), resolution = '驗貨鎖序'
		WHERE id = $1`, requestID); err != nil {
		t.Fatalf("approve return: %v", err)
	}
	return requestID, number, lines
}

func waitForLock(t *testing.T, name string, done <-chan error) {
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
		case err := <-done:
			t.Fatalf("inspection on %s finished before it waited on the order: %v", name, err)
		case <-deadline:
			t.Fatalf("inspection on %s never waited on the order", name)
		case <-tick.C:
		}
	}
}
