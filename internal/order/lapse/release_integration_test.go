//go:build integration

package lapse_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/order/ordertest"
)

// TestReleaseReservationLockOrder: release_reservation locks the order row
// before the reservation, the order cancellation and expiry sweeping share, so
// the two cannot close an order-reservation deadlock cycle. It holds the shared
// lock order between expiry sweeping and cancellation. Cancellation already
// owns the order row when it calls release_reservation; a sweeper that owned
// the reservation first could close an order↔reservation deadlock cycle.
func TestReleaseReservationLockOrder(t *testing.T) {
	ctx := t.Context()
	variantID := ordertest.FreshVariant(t, pool, "release-lock-order")
	orderID := ordertest.HeldOrder(t, pool, variantID, time.Hour, false)
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

	releasePool := storeApplicationPool(t, "release-order-before-reservation")
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
