//go:build integration

package payment_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/payment"
)

// TestStore_OrderEarliestHold: The session expiry comes from the earliest live
// hold. It binds payment to stock: the EARLIEST hold is the deadline, never the
// latest.
func TestStore_OrderEarliestHold(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, id := holdableOrder(t, 60000)

	o, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if !o.HoldExpiresAt.IsZero() {
		t.Errorf("an order holding nothing reports a hold until %v", o.HoldExpiresAt)
	}

	late := hold(t, id, 0, 45*time.Minute, "late:"+number)
	early := hold(t, id, 1, 35*time.Minute, "early:"+number)
	if !early.Before(late) {
		t.Fatalf("fixture expiry order is early=%v, late=%v", early, late)
	}

	o, err = s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order after the holds: %v", err)
	}
	if !o.HoldExpiresAt.Equal(early) {
		t.Errorf("the hold deadline is %v, want the EARLIER of the two holds, %v",
			o.HoldExpiresAt, early)
	}
	if !o.SessionExpiry().Equal(early) {
		t.Errorf("SessionExpiry() is %v, want %v — Stripe would go on taking money "+
			"after the stock was released", o.SessionExpiry(), early)
	}
}

// TestOrderHoldExpiryClock: OrderHoldExpiry judges hold admission by the
// database transaction clock, not the process clock.
func TestOrderHoldExpiryClock(t *testing.T) {
	ctx := t.Context()
	_, orderID := holdableOrder(t, 10000)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin clock transaction: %v", err)
	}
	cleanupCtx := context.WithoutCancel(t.Context())
	defer func() { _ = tx.Rollback(cleanupCtx) }()

	expires := hold(t, orderID, 0, 150*time.Millisecond, "db-clock:"+uuid.NewString())
	time.Sleep(250 * time.Millisecond)
	if !expires.Before(time.Now().Add(50 * time.Millisecond)) {
		t.Fatal("fixture did not cross the process-clock admission boundary")
	}
	row, err := db.New(tx).OrderHoldExpiry(ctx, db.OrderHoldExpiryParams{
		OrderID: orderID,
		RequiredLifetime: pgtype.Interval{
			Microseconds: (50 * time.Millisecond).Microseconds(), Valid: true,
		},
	})
	if err != nil {
		t.Fatalf("read hold against transaction clock: %v", err)
	}
	if !row.CoversSession {
		t.Fatal("hold admission was recomputed against the process clock instead of DB now()")
	}
}
