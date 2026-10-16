//go:build integration

package cart_test

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/order/buyer"
)

// TestACancelledOrdersStockComesBackByEveryDoor. Cancelled is SETTLED and not
// committed: reading any non-pending status as committed puts a cancelled order
// on the wrong side of both doors, and its units come back by no route at all.
func TestACancelledOrdersStockComesBackByEveryDoor(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-14")

	orderID := heldOrder(t, vid, time.Hour, true) // expired hold, paid
	refundAndCancel(t, orderID)

	var before int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&before); err != nil {
		t.Fatalf("read stock: %v", err)
	}

	released, _, err := s.Sweep(ctx, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if released == 0 {
		t.Fatal("the sweeper released nothing — a cancelled order's hold is still unreachable")
	}

	var after int32
	var state string
	if err := pool.QueryRow(ctx, `
		SELECT pv.stock_quantity,
		       (SELECT r.state FROM inventory_reservations r WHERE r.order_id = $2 LIMIT 1)
		FROM product_variants pv WHERE pv.id = $1`, vid, orderID).Scan(&after, &state); err != nil {
		t.Fatalf("read stock after: %v", err)
	}
	if after != before+1 {
		t.Errorf("stock is %d after the sweep, want %d", after, before+1)
	}
	if state != "released" {
		t.Errorf("the hold is %s, want released", state)
	}
}

// refundAndCancel cancels a paid order the one way the schema admits: a full
// refund before shipment whose card refund has settled.
func refundAndCancel(t *testing.T, orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	var staff, returnID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role)
		VALUES ('refund-' || gen_random_uuid() || '@goen.invalid', 'staff') RETURNING id`).Scan(&staff); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT open_refund_before_shipment(order_number, 'test', $2, 'test')
		FROM orders WHERE id = $1`, orderID, staff).Scan(&returnID); err != nil {
		t.Fatalf("refund before shipment: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO refunds (payment_id, return_request_id, request_key, status, amount_cents,
		                      reason, provider_ref, succeeded_at)
		 SELECT p.id, r.id, 'return:' || r.id, 'succeeded', r.card_refund_cents,
		        r.resolution, 're_' || r.id, now()
		 FROM return_requests r JOIN payments p ON p.order_id = r.order_id AND p.status = 'succeeded'
		 WHERE r.id = $1`,
		`INSERT INTO order_events (order_id, kind, note, return_request_id)
		 SELECT order_id, 'refunded', 're_' || id, id FROM return_requests WHERE id = $1`,
		`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
		 WHERE id = (SELECT order_id FROM return_requests WHERE id = $1)`,
	} {
		if _, err := pool.Exec(ctx, stmt, returnID); err != nil {
			t.Fatalf("settle the refund and cancel: %v", err)
		}
	}
}

func TestTheShoppersTimelineNeverShowsTheProvidersRefundID(t *testing.T) {
	ctx := t.Context()
	vid := freshVariant(t, "refundid-1")
	orderID := heldOrderFor(t, uuid.NullUUID{}, vid, -time.Hour, true)
	refundAndCancel(t, orderID)

	var number, staffNote string
	if err := pool.QueryRow(ctx, `
		SELECT o.order_number, e.note FROM orders o
		JOIN order_events e ON e.order_id = o.id AND e.kind = 'refunded'
		WHERE o.id = $1`, orderID).Scan(&number, &staffNote); err != nil || !strings.HasPrefix(staffNote, "re_") {
		t.Fatalf("the fixture must keep the refund id on the event: %q, %v", staffNote, err)
	}
	view, err := buyer.NewStore(pool).Order(ctx, number)
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	var sawRefund bool
	for _, e := range view.Timeline {
		if e.Kind == "refunded" {
			sawRefund = true
		}
		if strings.Contains(e.Note, "re_") {
			t.Errorf("%s event shows the provider refund id %q", e.Kind, e.Note)
		}
	}
	if !sawRefund {
		t.Error("the refunded event is missing from the shopper's timeline")
	}
}

func TestACancelledOrderIsNotAVerifiedPurchase(t *testing.T) {
	ctx := t.Context()
	vid := freshVariant(t, "stockfix-15")

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ('cancelbadge@example.com') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	orderID := heldOrderFor(t, uuid.NullUUID{UUID: userID, Valid: true}, vid, -time.Hour, true) // funded

	var productID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT product_id FROM product_variants WHERE id = $1`, vid).Scan(&productID); err != nil {
		t.Fatalf("read product: %v", err)
	}
	review := func() error {
		_, err := pool.Exec(ctx, `
			INSERT INTO product_reviews (product_id, user_id, rating, body, is_verified_purchase)
			VALUES ($1, $2, 5, '測試評價', true)
			ON CONFLICT (product_id, user_id) DO UPDATE SET is_verified_purchase = true`,
			productID, userID)
		return err
	}

	if err := review(); err != nil {
		t.Fatalf("a real purchase could not claim 已購買: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM product_reviews WHERE product_id = $1 AND user_id = $2`,
		productID, userID); err != nil {
		t.Fatalf("clear review: %v", err)
	}

	refundAndCancel(t, orderID)
	err := review()
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "product_reviews_verified_is_real" {
		t.Errorf("a cancelled order still earned 已購買: %v", err)
	}
}
