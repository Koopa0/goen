//go:build integration

package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ordernotice"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/web"
)

func terminalNotice(t *testing.T, id uuid.UUID, want ordernotice.Kind) {
	t.Helper()
	var payload []byte
	if err := pool.QueryRow(t.Context(), `SELECT payload FROM outbox_messages WHERE topic=$1 AND dedupe_key=$2`, outbox.TopicOrderTerminal, id.String()+":"+string(want)).Scan(&payload); err != nil {
		t.Fatalf("missing terminal notice: %v", err)
	}
	var fields map[string]string
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 || fields["order_id"] != id.String() || fields["kind"] != string(want) {
		t.Fatalf("notice contains wrong event or private data: %s", payload)
	}
}

func TestTerminalCancellationNoticesFollowCommittedActor(t *testing.T) {
	ctx, _ := staffContext(t)
	number, id, _ := pendingOrderHoldingStock(t)
	if _, err := admin.NewStore(pool, fakeRefunder{}, nil, nil).Advance(ctx, number, "cancelled", uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	terminalNotice(t, id, ordernotice.CancelledByStaff)
	number, id, _ = pendingOrderHoldingStock(t)
	if _, err := cart.NewStore(pool).Cancel(t.Context(), number); err != nil {
		t.Fatal(err)
	}
	terminalNotice(t, id, ordernotice.CancelledByCustomer)
	if _, err := cart.NewStore(pool).Cancel(t.Context(), number); !errors.Is(err, cart.ErrNotCancellable) {
		t.Fatalf("repeat cancellation=%v", err)
	}
}

func TestTerminalArrivalIsNotRequeuedAfterCompletionOrOutboxRetention(t *testing.T) {
	ctx, _ := staffContext(t)
	number, id := pickingOrderHoldingStock(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	if err := s.Ship(ctx, number, admin.Dispatch{Carrier: "terminal", Tracking: uuid.NewString()}, uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(ctx, number, "delivered", uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	terminalNotice(t, id, ordernotice.Delivered)
	if _, err := pool.Exec(t.Context(), `DELETE FROM outbox_messages WHERE topic=$1 AND dedupe_key=$2`, outbox.TopicOrderTerminal, id.String()+":"+string(ordernotice.Delivered)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(ctx, number, "completed", uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_messages WHERE topic=$1 AND payload->>'order_id'=$2`, outbox.TopicOrderTerminal, id.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("completion sent a second arrival after retention")
	}
}

func TestFailedAdvanceRollsBackItsTerminalNotice(t *testing.T) {
	number, id, _ := pendingOrderHoldingStock(t)
	ctx := web.WithRequestID(account.WithUser(t.Context(), account.User{ID: uuid.NewString(), Role: "admin"}), "missing-audit-actor")
	if _, err := admin.NewStore(pool, fakeRefunder{}, nil, nil).Advance(ctx, number, "cancelled", uuid.NullUUID{}); err == nil {
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
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_messages WHERE topic=$1 AND payload->>'order_id'=$2`, outbox.TopicOrderTerminal, id.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed advance queued a notice")
	}
}

func TestTerminalRecipientNeverRecoversErasedAddress(t *testing.T) {
	_, id, _ := pendingOrderHoldingStock(t)
	q := db.New(pool)
	if _, err := q.TerminalOrderRecipient(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE order_private_data SET email=NULL,recipient_name=NULL,phone=NULL,postal_code=NULL,city=NULL,district=NULL,street=NULL,erased_at=now() WHERE order_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := q.TerminalOrderRecipient(t.Context(), id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("erased recipient resolved: %v", err)
	}
}

func TestPickupCompletionQueuesCollectionWithoutADeliveryNotice(t *testing.T) {
	ctx, _ := staffContext(t)
	number, id, _ := pendingOrderHoldingStock(t)
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
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	if err := s.Ship(ctx, number, admin.Dispatch{Carrier: "pickup", Tracking: uuid.NewString()}, uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(ctx, number, "completed", uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	terminalNotice(t, id, ordernotice.Collected)
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_messages WHERE topic=$1 AND payload->>'order_id'=$2`, outbox.TopicOrderTerminal, id.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("pickup queued %d terminal notices", count)
	}
}
