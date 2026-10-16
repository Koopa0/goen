//go:build integration

package cart_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/order/delivery"
)

func TestTheConfirmationMessageCommitsWithTheOrder(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := variantOf(t, "koto-over-ear", true)

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &delivery.Details{
		Email: "ob@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "路 1 號",
	}

	id := newCart(t, s)
	if err := s.Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	number, err := placeOrder(t, s, ctx, id, uuid.NullUUID{}, shipID, addr, "", "outbox-ok")
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	var messages int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE topic = 'order.placed' AND dedupe_key = $1`, number).Scan(&messages); err != nil {
		t.Fatalf("count: %v", err)
	}
	if messages != 1 {
		t.Errorf("%d messages for a placed order, want 1", messages)
	}

	// A fabricated shipping version is refused AFTER the transaction has begun,
	// which is the window a message written outside it would survive.
	before := countMessages(t)
	failed := newCart(t, s)
	if err := s.Add(ctx, failed, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := s.PlaceOrder(
		ctx, failed, uuid.NullUUID{}, uuid.New(), addr, nil, "", cart.CheckoutQuoteID{},
		checkoutAttemptKey("outbox-fail"),
	); err == nil {
		t.Fatal("a fabricated shipping version was accepted")
	}
	if after := countMessages(t); after != before {
		t.Errorf("a failed checkout left %d new messages; the enqueue is not in "+
			"the order's transaction", after-before)
	}
}

// countMessages is how many order.placed messages exist.
func countMessages(t *testing.T) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM outbox_messages WHERE topic = 'order.placed'`).Scan(&n); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	return n
}
