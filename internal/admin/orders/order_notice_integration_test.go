//go:build integration

package orders_test

import (
	"context"
	"errors"
	"testing"

	"github.com/koopa0/goen/internal/outbox"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
)

func terminalNotice(t *testing.T, id uuid.UUID, want email.TerminalKind) {
	t.Helper()
	admintest.AssertTerminalNotice(t, pool, id, want, false)
}

func TestTerminalCancellationNoticesFollowCommittedActor(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	number, id, _ := admintest.PendingOrderHoldingStock(t, pool)
	if _, err := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil).Advance(ctx, number, "cancelled", uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	terminalNotice(t, id, email.TerminalCancelledByStaff)
	number, id, _ = admintest.PendingOrderHoldingStock(t, pool)
	if err := cart.NewStore(pool).CancelOrder(t.Context(), number); err != nil {
		t.Fatal(err)
	}
	terminalNotice(t, id, email.TerminalCancelledByCustomer)
	if err := cart.NewStore(pool).CancelOrder(t.Context(), number); !errors.Is(err, cart.ErrNotCancellable) {
		t.Fatalf("repeat cancellation=%v", err)
	}
}

func TestTerminalArrivalIsNotRequeuedAfterCompletionOrOutboxRetention(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	number, id := pickingOrderHoldingStock(t)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	if err := s.Ship(ctx, number, orders.Dispatch{Carrier: "black_cat", Tracking: uuid.NewString()}, uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(ctx, number, "delivered", uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	terminalNotice(t, id, email.TerminalDelivered)
	if _, err := pool.Exec(t.Context(), `DELETE FROM outbox_messages WHERE topic=$1 AND dedupe_key=$2`, outbox.TopicOrderTerminal.Name(), id.String()+":"+string(email.TerminalDelivered)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(ctx, number, "completed", uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_messages WHERE topic=$1 AND payload->>'order_id'=$2`, outbox.TopicOrderTerminal.Name(), id.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("completion sent a second arrival after retention")
	}
}

func TestFailedAdvanceRollsBackItsTerminalNotice(t *testing.T) {
	number, id, _ := admintest.PendingOrderHoldingStock(t, pool)
	ctx := web.WithRequestID(user.NewContext(t.Context(), user.User{ID: uuid.NewString(), Role: user.RoleAdmin}), "missing-audit-actor")
	if _, err := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil).Advance(ctx, number, "cancelled", uuid.NullUUID{}); err == nil {
		t.Fatal("advance accepted missing audit actor")
	}
	var status string
	if err := pool.QueryRow(t.Context(), `SELECT fulfillment_status FROM orders WHERE id=$1`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("failed advance committed %s", status)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_messages WHERE topic=$1 AND payload->>'order_id'=$2`, outbox.TopicOrderTerminal.Name(), id.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed advance queued a notice")
	}
}

func TestPickupCompletionQueuesCollectionWithoutADeliveryNotice(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	number, id, _ := admintest.PendingOrderHoldingStock(t, pool)
	if _, err := pool.Exec(t.Context(), `UPDATE orders SET shipping_version_id=v.id,shipping_method_code=sm.code,shipping_method_name=v.name FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id=v.method_id WHERE orders.id=$1 AND sm.destination_kind='pickup_point'`, id); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	if _, err := tx.Exec(t.Context(), `SELECT open_payment($1,$2,100000)`, id, "terminal-"+number); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `SELECT capture_payment($1,100000,NULL,NULL)`, "terminal-"+number); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `UPDATE orders SET fulfillment_status='picking' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	if err := s.Ship(ctx, number, orders.Dispatch{Carrier: "seven_eleven", Tracking: uuid.NewString()}, uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(ctx, number, "delivered", uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	var early int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_messages WHERE topic=$1 AND payload->>'order_id'=$2`, outbox.TopicOrderTerminal.Name(), id.String()).Scan(&early); err != nil {
		t.Fatal(err)
	}
	if early != 0 {
		t.Fatalf("a pickup order marked delivered queued %d notices before it was collected", early)
	}
	if _, err := s.Advance(ctx, number, "completed", uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	terminalNotice(t, id, email.TerminalCollected)
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_messages WHERE topic=$1 AND payload->>'order_id'=$2`, outbox.TopicOrderTerminal.Name(), id.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("pickup queued %d terminal notices", count)
	}
}
