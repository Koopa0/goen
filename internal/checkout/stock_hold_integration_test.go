//go:build integration

package checkout_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/order/delivery"
)

func TestAnInfrastructureFailureIsNotReportedAsSoldOut(t *testing.T) {
	ctx := t.Context()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse timeout pool config: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "1500"
	timeoutPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open timeout pool: %v", err)
	}
	t.Cleanup(timeoutPool.Close)
	s := checkout.NewStore(timeoutPool)
	id := newCart(t, s)
	vid := freshVariant(t, "infra-is-not-sold-out")
	if addErr := s.Carts().Add(ctx, id, vid, 1); addErr != nil {
		t.Fatalf("add: %v", addErr)
	}

	blocker, err := pgx.Connect(ctx, pool.Config().ConnString())
	if err != nil {
		t.Fatalf("open blocker: %v", err)
	}
	t.Cleanup(func() { _ = blocker.Close(context.Background()) }) //nolint:usetesting // cleanup runs after t.Context is canceled
	tx, err := blocker.Begin(ctx)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, lockErr := tx.Exec(ctx, `LOCK TABLE inventory_movements IN ACCESS EXCLUSIVE MODE`); lockErr != nil {
		t.Fatalf("lock inventory ledger: %v", lockErr)
	}

	addr := &delivery.Details{
		Email: "infra@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	_, err = placeOrder(t, s, ctx, id, uuid.NullUUID{}, shipVersionFor(t, "home_delivery"),
		addr, "", "infra-not-sold-out-"+uuid.NewString())
	if errors.Is(err, cart.ErrUnavailable) {
		t.Fatalf("a statement timeout was reported as sold-out inventory: %v", err)
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.Code != "57014" {
		t.Fatalf("hold failure = %v, want preserved PgError 57014", err)
	}
	if !strings.Contains(err.Error(), "hold 1 of variant "+vid.String()) {
		t.Fatalf("timeout landed outside the intended inventory hold: %v", err)
	}
	// The handler branches on this: without it the customer's whole checkout
	// form is discarded behind a 500, and every pool now carries a
	// statement_timeout so the wait that produces it is ordinary rather than
	// exceptional. The PgError above must survive the wrap, or a caller can no
	// longer tell WHICH statement the database ended.
	if !errors.Is(err, checkout.ErrBusy) {
		t.Fatalf("a cancelled statement = %v, want ErrBusy so checkout re-renders at 422", err)
	}
}

func TestInventoryConstraintIsReportedAsSoldOut(t *testing.T) {
	ctx := t.Context()
	app := "wave0-inventory-" + uuid.NewString()
	appPool := applicationPool(t, app)
	s := checkout.NewStore(appPool)
	vid := freshVariant(t, "named-inventory-refusal")
	if _, err := pool.Exec(ctx, `
		SELECT record_inventory_movement($1, -9, 'adjustment', $2, 'admin', NULL, NULL)`,
		vid, "leave-one:"+vid.String()); err != nil {
		t.Fatalf("leave one unit: %v", err)
	}
	id := newCart(t, s)
	if err := s.Carts().Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add last unit: %v", err)
	}

	blockOrder := commitBareOrder(t, vid)
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(ctx) }()
	if _, err := blocker.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, interval '30 minutes', $3)`,
		blockOrder, vid, "take-last:"+vid.String()); err != nil {
		t.Fatalf("hold the last unit: %v", err)
	}

	addr := &delivery.Details{
		Email: "soldout@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	shipID := shipVersionFor(t, "home_delivery")
	done := make(chan error, 1)
	go func() {
		_, placeErr := placeOrder(t, s, ctx, id, uuid.NullUUID{}, shipID,
			addr, "", "named-inventory-"+uuid.NewString())
		done <- placeErr
	}()
	waitForApplicationLock(t, app, done)
	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("commit competing hold: %v", err)
	}
	placeErr := <-done
	if !errors.Is(placeErr, cart.ErrUnavailable) {
		t.Fatalf("inventory_never_negative mapped to %v, want ErrUnavailable", placeErr)
	}

	// Bind the fixture's exhausted state to the database's typed answer. The
	// store intentionally returns the bare domain sentinel after checking this
	// name, so the PgError itself is verified through the same production door.
	_, namedErr := pool.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, interval '30 minutes', $3)`,
		commitBareOrder(t, vid), vid, "prove-empty:"+vid.String())
	pgErr, ok := errors.AsType[*pgconn.PgError](namedErr)
	if !ok || pgErr.ConstraintName != "inventory_never_negative" {
		t.Fatalf("exhausted fixture was refused by %v, want inventory_never_negative", namedErr)
	}
}

func TestCheckoutHoldsStock(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)

	vid := variantOf(t, "koto-over-ear")
	var wasStock, wasSafety int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity, safety_stock FROM product_variants WHERE id = $1`,
		vid).Scan(&wasStock, &wasSafety); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is already cancelled in Cleanup
		_, _ = pool.Exec(context.Background(),
			`UPDATE product_variants SET stock_quantity = $2, safety_stock = $3 WHERE id = $1`,
			vid, wasStock, wasSafety)
	})
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET stock_quantity = 3, safety_stock = 2 WHERE id = $1`,
		vid); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &delivery.Details{
		Email: "hold@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}

	// The delta, not the absolute figure: other tests in this package hold stock too.
	var before int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&before); err != nil {
		t.Fatalf("read stock: %v", err)
	}

	id := newCart(t, s)
	if err := s.Carts().Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	number, err := placeOrder(t, s, ctx, id, uuid.NullUUID{}, shipID, addr, "", "hold-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	var after int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&after); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if before-after != 1 {
		t.Errorf("stock went %d to %d over an order for one unit; the order held %d",
			before, after, before-after)
	}

	var reserved int32
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(ir.quantity), 0) FROM inventory_reservations ir
		JOIN orders o ON o.id = ir.order_id
		WHERE o.order_number = $1 AND ir.variant_id = $2 AND ir.state = 'held'`,
		number, vid).Scan(&reserved); err != nil {
		t.Fatalf("read reservations: %v", err)
	}
	if reserved != 1 {
		t.Errorf("order %s holds %d units, want 1", number, reserved)
	}

	var movements int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM inventory_movements
		WHERE variant_id = $1 AND reason = 'hold' AND idempotency_key LIKE 'hold:%'
		  AND source_id = (SELECT id FROM orders WHERE order_number = $2)`,
		vid, number).Scan(&movements); err != nil {
		t.Fatalf("read movements: %v", err)
	}
	if movements != 1 {
		t.Errorf("%d movements recorded for the hold, want 1", movements)
	}
}

func TestCheckoutLineHoldsShareTheOrderDeadline(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	a, b, _ := threeVariants(t, "shared-hold-deadline")
	cartID := newCart(t, s)
	for _, variantID := range []uuid.UUID{a, b} {
		if err := s.Carts().Add(ctx, cartID, variantID, 1); err != nil {
			t.Fatalf("add variant %s: %v", variantID, err)
		}
	}

	shippingID := shipVersionFor(t, "home_delivery")
	addr := &delivery.Details{
		Email: "shared-hold@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	number, err := placeOrder(t, s, ctx, cartID, uuid.NullUUID{}, shippingID, addr, "",
		"shared-hold-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place order: %v", err)
	}

	var holds, deadlines, wantMinutes int
	var seconds float64
	if readErr := pool.QueryRow(ctx, `
		SELECT count(*)::integer, count(DISTINCT ir.expires_at)::integer,
		       extract(epoch FROM min(ir.expires_at) - min(o.placed_at))::float8,
		       checkout_hold_minutes()
		FROM inventory_reservations ir
		JOIN orders o ON o.id = ir.order_id
		WHERE o.order_number = $1`, number).Scan(&holds, &deadlines, &seconds, &wantMinutes); readErr != nil {
		t.Fatalf("read hold deadlines: %v", readErr)
	}
	if holds != 2 || deadlines != 1 {
		t.Errorf("order has %d holds across %d deadlines, want 2 holds sharing 1 deadline", holds, deadlines)
	}
	if want := float64(wantMinutes * 60); seconds != want {
		t.Errorf("hold deadline is %.6f seconds after placement, want %.0f", seconds, want)
	}
}

// TestTwoOrdersCannotTakeTheSameLastUnit is the race the hold exists to lose
// safely. T1's transaction is held OPEN while T2 runs, and both orders are
// created first because next_order_number() locks the per-day counter row.
func TestTwoOrdersCannotTakeTheSameLastUnit(t *testing.T) {
	ctx := t.Context()

	vid := variantOf(t, "nimbus-band-2")
	var wasStock, wasSafety int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity, safety_stock FROM product_variants WHERE id = $1`,
		vid).Scan(&wasStock, &wasSafety); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is already cancelled in Cleanup
		_, _ = pool.Exec(context.Background(),
			`UPDATE product_variants SET stock_quantity = $2, safety_stock = $3 WHERE id = $1`,
			vid, wasStock, wasSafety)
	})
	// Exactly one unit may be sold: 3 on hand, floor of 2.
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET stock_quantity = 3, safety_stock = 2 WHERE id = $1`,
		vid); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	order1 := commitBareOrder(t, vid)
	order2 := commitBareOrder(t, vid)

	tx1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin t1: %v", err)
	}
	defer func() { _ = tx1.Rollback(ctx) }()
	if _, holdErr := tx1.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, interval '30 minutes', $3)`,
		order1, vid, "race-t1"); holdErr != nil {
		t.Fatalf("t1 hold: %v", holdErr)
	}

	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin t2: %v", err)
	}
	defer func() { _ = tx2.Rollback(ctx) }()

	// T2's backend id, read BEFORE the racing statement.
	var pid int
	if err := tx2.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("read t2 pid: %v", err)
	}

	done := make(chan error, 1)
	// t.Context() and not context.Background(): the goroutine must be cancelled
	// with the test.
	raceCtx := t.Context()
	go func() {
		_, holdErr := tx2.Exec(raceCtx,
			`SELECT hold_inventory($1, $2, 1, interval '30 minutes', $3)`,
			order2, vid, "race-t2")
		done <- holdErr
	}()

	// Wait until T2 is ACTUALLY blocked rather than pausing: on a loaded machine
	// T1 would commit first and the case would prove nothing about contention.
	waitUntilBlocked(t, pid, done)
	if err := tx1.Commit(ctx); err != nil {
		t.Fatalf("commit t1: %v", err)
	}

	select {
	case holdErr := <-done:
		if holdErr == nil {
			t.Fatal("both orders held the same last unit; the stock floor did not bite")
		}
		// WHICH rule refused matters, and the name is on PgError rather than in the
		// message text.
		pgErr, ok := errors.AsType[*pgconn.PgError](holdErr)
		if !ok || pgErr.ConstraintName != "inventory_never_negative" {
			t.Errorf("t2 was refused by %v, want constraint inventory_never_negative", holdErr)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("t2 never returned; the two holds are not contending for the same row")
	}
}

// commitBareOrder writes the minimum an order needs to exist, one of vid, and
// commits it. The line is what lets the order hold vid: hold_inventory holds
// nothing an order's own lines do not carry.
func commitBareOrder(t *testing.T, vid uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code, shipping_method_name)
		SELECT next_order_number(), v.id, sm.code, v.name
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		SELECT $1, pv.id, pv.sku, p.name, 100000, 1
		FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE pv.id = $2`, id, vid); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'r@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		id); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit order: %v", err)
	}
	return id
}

// TestReservationReleaseLocksOrderBeforeReservation holds the shared lock
// order between expiry sweeping and cancellation. Cancellation already owns
// the order row when it calls release_reservation; a sweeper that owned the
// reservation first could close an order↔reservation deadlock cycle.
func TestReservationReleaseLocksOrderBeforeReservation(t *testing.T) {
	ctx := t.Context()
	variantID := freshVariant(t, "release-lock-order")
	orderID := heldOrder(t, variantID, time.Hour, false)
	var reservationID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT id FROM inventory_reservations
		WHERE order_id = $1 AND state = 'held'`, orderID).Scan(&reservationID); err != nil {
		t.Fatalf("read held reservation: %v", err)
	}

	orderTx, beginErr := pool.Begin(ctx)
	if beginErr != nil {
		t.Fatalf("begin order blocker: %v", beginErr)
	}
	defer func() { _ = orderTx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := orderTx.Exec(ctx,
		`SELECT 1 FROM orders WHERE id = $1 FOR UPDATE`, orderID); err != nil {
		t.Fatalf("lock reservation order: %v", err)
	}

	releasePool := applicationPool(t, "release-order-before-reservation")
	releaseDone := make(chan error, 1)
	go func() {
		_, releaseErr := releasePool.Exec(ctx,
			`SELECT release_reservation($1)`, reservationID)
		releaseDone <- releaseErr
	}()
	waitForApplicationLock(t, "release-order-before-reservation", releaseDone)

	probe, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin reservation probe: %v", err)
	}
	if _, err := probe.Exec(ctx, `
		SELECT 1 FROM inventory_reservations WHERE id = $1 FOR UPDATE NOWAIT`, reservationID); err != nil {
		_ = probe.Rollback(ctx)
		t.Fatalf("release locked reservation before its order: %v", err)
	}
	if err := probe.Rollback(ctx); err != nil {
		t.Fatalf("release reservation probe: %v", err)
	}

	if err := orderTx.Commit(ctx); err != nil {
		t.Fatalf("release order blocker: %v", err)
	}
	if err := <-releaseDone; err != nil {
		t.Fatalf("release reservation after order unlock: %v", err)
	}
}

// TestInventoryHoldLocksOrderBeforeVariant proves the other half of the shared
// order -> reservation/variant contract. Merely relying on the reservation's
// foreign key would take its weak order lock only after stock had been locked,
// recreating the release/re-hold ABBA cycle.
func TestInventoryHoldLocksOrderBeforeVariant(t *testing.T) {
	ctx := t.Context()
	variantID := freshVariant(t, "hold-lock-order")
	orderID := heldOrder(t, variantID, time.Hour, false)
	var oldReservationID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT id FROM inventory_reservations
		WHERE order_id = $1 AND state = 'held'`, orderID).Scan(&oldReservationID); err != nil {
		t.Fatalf("read initial reservation: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT release_reservation($1)`, oldReservationID); err != nil {
		t.Fatalf("release initial reservation: %v", err)
	}

	orderTx, beginErr := pool.Begin(ctx)
	if beginErr != nil {
		t.Fatalf("begin order blocker: %v", beginErr)
	}
	defer func() { _ = orderTx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := orderTx.Exec(ctx,
		`SELECT 1 FROM orders WHERE id = $1 FOR UPDATE`, orderID); err != nil {
		t.Fatalf("lock hold order: %v", err)
	}

	holdPool := applicationPool(t, "hold-order-before-variant")
	holdDone := make(chan error, 1)
	go func() {
		_, holdErr := holdPool.Exec(ctx, `
			SELECT hold_inventory($1, $2, 1, interval '30 minutes', $3)`,
			orderID, variantID, "lock-order-rehold:"+uuid.NewString())
		holdDone <- holdErr
	}()
	waitForApplicationLock(t, "hold-order-before-variant", holdDone)

	probe, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin variant probe: %v", err)
	}
	if _, err := probe.Exec(ctx, `
		SELECT 1 FROM product_variants WHERE id = $1 FOR UPDATE NOWAIT`, variantID); err != nil {
		_ = probe.Rollback(ctx)
		t.Fatalf("hold locked the variant before its order: %v", err)
	}
	if err := probe.Rollback(ctx); err != nil {
		t.Fatalf("release variant probe: %v", err)
	}

	if err := orderTx.Commit(ctx); err != nil {
		t.Fatalf("release order blocker: %v", err)
	}
	if err := <-holdDone; err != nil {
		t.Fatalf("hold inventory after order unlock: %v", err)
	}
}

// waitUntilBlocked returns once the backend at pid is waiting on a lock, or once
// done fires. pg_stat_activity is the synchronisation point, so the interleaving
// is a fact rather than an assumption; the 5ms is a poll interval, not a guess.
func waitUntilBlocked(t *testing.T, pid int, done <-chan error) {
	t.Helper()
	ctx := t.Context()
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case <-done:
			// T2 finishing while T1 still held its row means hold_inventory never took the
			// lock, and without this branch the case would pass anyway.
			t.Fatal("the second writer finished without ever blocking; nothing " +
				"serialised the two, so the row lock is not being taken")
		default:
		}
		if _, waiting, err := dbtest.Blocked(ctx, pool, dbtest.Match{PID: int32(pid)}); err == nil && waiting { //nolint:gosec // G115: a backend pid is an int4 in pg_backend_pid()
			return
		}
		if time.Now().After(deadline) {
			var state, waitType, waitEvent, query string
			if seen, _ := dbtest.Backends(ctx, pool, dbtest.Match{PID: int32(pid)}); len(seen) == 1 { //nolint:gosec // G115: a backend pid is an int4 in pg_backend_pid()
				state, waitType, waitEvent, query = seen[0].State, seen[0].WaitType, seen[0].WaitEvent, seen[0].Query
			}
			t.Fatalf("the second writer neither finished nor blocked on a lock; "+
				"pid=%d state=%q wait=%q/%q query=%q", pid, state, waitType, waitEvent, query)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
