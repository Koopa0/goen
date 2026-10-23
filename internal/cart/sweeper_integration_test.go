//go:build integration

package cart_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/order/lapse"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// TestReleaseReservationFundedOrder holds that release_reservation refuses a
// fully credit-funded order's hold, by name.
func TestReleaseReservationFundedOrder(t *testing.T) {
	ctx := t.Context()
	vid := ordertest.FreshVariant(t, pool, "stockfix-credit-door")
	orderID := ordertest.CreditFundedHeldOrder(t, pool, vid, time.Hour)

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
