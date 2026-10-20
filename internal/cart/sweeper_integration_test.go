//go:build integration

package cart_test

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/order/lapse"
)

func TestSweepReturnsAbandonedHoldsToTheShelf(t *testing.T) {
	ctx := t.Context()
	s := lapse.NewSweeper(pool)
	vid := freshVariant(t, "stockfix-2")

	var before int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&before); err != nil {
		t.Fatalf("read stock: %v", err)
	}

	orderID := heldOrder(t, vid, time.Hour, false)

	var mid int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&mid); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if before-mid != 1 {
		t.Fatalf("holding one unit moved stock %d to %d; the fixture is wrong", before, mid)
	}

	if _, _, err := s.Sweep(ctx, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	var after int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&after); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if after != before {
		t.Errorf("stock is %d after sweeping an expired hold, want %d — the unit "+
			"never came back", after, before)
	}

	var state string
	if err := pool.QueryRow(ctx,
		`SELECT state FROM inventory_reservations WHERE order_id = $1`, orderID).Scan(&state); err != nil {
		t.Fatalf("read reservation: %v", err)
	}
	if state != "released" {
		t.Errorf("reservation is %q, want released", state)
	}

	var movements int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM inventory_movements
		WHERE variant_id = $1 AND reason = 'release'`, vid).Scan(&movements); err != nil {
		t.Fatalf("read movements: %v", err)
	}
	if movements == 0 {
		t.Error("stock came back with no movement recorded")
	}
}

// TestSweepNeverTouchesAPaidOrdersHold. The lock here is the database:
// release_reservation refuses a committed order's hold anyway, so the query
// filter this also exercises is defence in depth rather than a lock.
func TestSweepNeverTouchesAPaidOrdersHold(t *testing.T) {
	ctx := t.Context()
	s := lapse.NewSweeper(pool)
	vid := freshVariant(t, "stockfix-3")

	// Expired AND paid: without the expiry this passes with the funding check gone.
	paidOrder := heldOrder(t, vid, time.Hour, true)
	abandoned := heldOrder(t, vid, time.Hour, false)

	if _, _, err := s.Sweep(ctx, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	var paidState, abandonedState string
	if err := pool.QueryRow(ctx,
		`SELECT state FROM inventory_reservations WHERE order_id = $1`, paidOrder).Scan(&paidState); err != nil {
		t.Fatalf("read paid reservation: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT state FROM inventory_reservations WHERE order_id = $1`, abandoned).Scan(&abandonedState); err != nil {
		t.Fatalf("read abandoned reservation: %v", err)
	}
	if paidState != "held" {
		t.Errorf("a PAID order's hold is %q after a sweep, want held — its stock "+
			"is now sellable twice", paidState)
	}
	if abandonedState != "released" {
		t.Errorf("the abandoned hold is %q, want released", abandonedState)
	}
}

// TestSweepNeverTouchesAFullyCreditFundedOrdersHold is the half committed_orders
// cannot answer: a fully store-credited order has no payment row and cannot have
// one, so the view reports it uncommitted while the customer has paid in full.
func TestSweepNeverTouchesAFullyCreditFundedOrdersHold(t *testing.T) {
	ctx := t.Context()
	s := lapse.NewSweeper(pool)
	vid := freshVariant(t, "stockfix-credit")

	var before int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&before); err != nil {
		t.Fatalf("read stock: %v", err)
	}

	funded := creditFundedHeldOrder(t, vid, time.Hour)
	abandoned := heldOrder(t, vid, time.Hour, false)

	if _, _, err := s.Sweep(ctx, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	var fundedState, abandonedState string
	if err := pool.QueryRow(ctx,
		`SELECT state FROM inventory_reservations WHERE order_id = $1`, funded).Scan(&fundedState); err != nil {
		t.Fatalf("read funded reservation: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT state FROM inventory_reservations WHERE order_id = $1`, abandoned).Scan(&abandonedState); err != nil {
		t.Fatalf("read abandoned reservation: %v", err)
	}
	if fundedState != "held" {
		t.Errorf("a fully store-credited order's hold is %q after a sweep, want held — "+
			"the customer paid for it and its stock is now sellable twice", fundedState)
	}
	if abandonedState != "released" {
		t.Errorf("the abandoned hold is %q, want released", abandonedState)
	}

	var after int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&after); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if want := before - 1; after != want {
		t.Errorf("stock is %d after the sweep, want %d — the funded order's unit "+
			"went back on the shelf", after, want)
	}
}

func TestReleasingAFundedOrdersHoldIsRefusedByName(t *testing.T) {
	ctx := t.Context()
	vid := freshVariant(t, "stockfix-credit-door")
	orderID := creditFundedHeldOrder(t, vid, time.Hour)

	var reservationID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM inventory_reservations WHERE order_id = $1`, orderID).
		Scan(&reservationID); err != nil {
		t.Fatalf("read reservation: %v", err)
	}

	_, err := pool.Exec(ctx, `SELECT release_reservation($1)`, reservationID)
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "inventory_reservation_funded_no_release" {
		t.Fatalf("releasing a fully-funded order's hold = %v, want "+
			"inventory_reservation_funded_no_release", err)
	}
	if !lapse.BenignSweepFailure(err) {
		t.Errorf("the sweeper reads this refusal as a failure; it is the sweeper " +
			"being safe, and an operator cannot tell those apart if it logs as an error")
	}

	var state string
	if err := pool.QueryRow(ctx,
		`SELECT state FROM inventory_reservations WHERE id = $1`, reservationID).
		Scan(&state); err != nil {
		t.Fatalf("read reservation state: %v", err)
	}
	if state != "held" {
		t.Errorf("reservation is %q after a refused release, want held", state)
	}
}

func TestSweepLeavesUnexpiredHolds(t *testing.T) {
	ctx := t.Context()
	s := lapse.NewSweeper(pool)
	vid := freshVariant(t, "stockfix-4")

	// -(-10 minutes) is ten minutes in the FUTURE: still holding.
	fresh := heldOrder(t, vid, -10*time.Minute, false)

	if _, _, err := s.Sweep(ctx, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	var state string
	if err := pool.QueryRow(ctx,
		`SELECT state FROM inventory_reservations WHERE order_id = $1`, fresh).Scan(&state); err != nil {
		t.Fatalf("read reservation: %v", err)
	}
	if state != "held" {
		t.Errorf("an unexpired hold is %q after a sweep, want held — the customer "+
			"lost the item while paying for it", state)
	}
}

func TestSweepIsIdempotent(t *testing.T) {
	ctx := t.Context()
	s := lapse.NewSweeper(pool)
	vid := freshVariant(t, "stockfix-5")
	orderID := heldOrder(t, vid, time.Hour, false)

	first, firstSkipped, err := s.Sweep(ctx, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if first == 0 {
		t.Fatal("the first sweep released nothing; the fixture is wrong")
	}
	if firstSkipped != 0 {
		t.Errorf("the first sweep skipped %d, want 0", firstSkipped)
	}
	second, _, err := s.Sweep(ctx, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if second != 0 {
		t.Errorf("the second sweep released %d rows, want 0", second)
	}

	var releases int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM inventory_movements
		WHERE source_id = (SELECT id FROM inventory_reservations WHERE order_id = $1)
		  AND reason = 'release'`, orderID).Scan(&releases); err != nil {
		t.Fatalf("count movements: %v", err)
	}
	if releases != 1 {
		t.Errorf("%d release movements for one reservation, want 1 — the stock "+
			"came back more than once", releases)
	}
}

func TestSweepCountsABenignRefusalAsSkippedNotFailed(t *testing.T) {
	ctx := t.Context()
	vid := freshVariant(t, "stockfix-6")

	// Expired, and paid — the benign case: a sweeper whose query filter is gone
	// still sees it, and release_reservation refuses it.
	paid := heldOrder(t, vid, time.Hour, true)

	// Reach past the query, so the refusal happens rather than being filtered out.
	var reservationID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM inventory_reservations WHERE order_id = $1`, paid).Scan(&reservationID); err != nil {
		t.Fatalf("find reservation: %v", err)
	}
	_, err := pool.Exec(ctx, `SELECT release_reservation($1)`, reservationID)
	if err == nil {
		t.Fatal("release_reservation accepted a committed order's hold")
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "inventory_reservation_committed_no_release" {
		t.Fatalf("refused by %v, want inventory_reservation_committed_no_release", err)
	}
	if !lapse.BenignSweepFailure(err) {
		t.Error("the sweeper would count this refusal as a failure; it is the sweeper being safe")
	}

	if lapse.BenignSweepFailure(errors.New("connection reset")) {
		t.Error("an ordinary error was classified as benign")
	}
}

func TestTheAttemptSweepKeepsRecentKeys(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	plant := func(key, age string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO checkout_attempts (idempotency_key, created_at)
			VALUES ($1, now() - $2::interval)`, key, age); err != nil {
			t.Fatalf("plant %s: %v", key, err)
		}
	}
	old := checkoutAttemptKey("sweep-old-" + uuid.NewString())
	recent := checkoutAttemptKey("sweep-recent-" + uuid.NewString())
	plant(old, "60 days")
	plant(recent, "1 hour")

	if err := s.SweepAttempts(ctx); err != nil {
		t.Fatalf("SweepAttempts: %v", err)
	}

	for key, want := range map[string]bool{old: false, recent: true} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM checkout_attempts WHERE idempotency_key = $1)`,
			key).Scan(&exists); err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		if exists != want {
			t.Errorf("%s: exists = %v, want %v", key, exists, want)
		}
	}
}
