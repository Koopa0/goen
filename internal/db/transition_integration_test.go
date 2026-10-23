//go:build integration

package db_test

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/order"
)

// TestOrdersLegalTransitionMirrorsNext: Next is the transitions the trigger
// allows. The trigger's legality check raises before every precondition
// (payment, refund, invoice, parcels), so a refusal under any other constraint
// means the transition table itself let the pair through.
func TestOrdersLegalTransitionMirrorsNext(t *testing.T) {
	for _, from := range order.FulfillmentStatuses {
		for _, to := range order.FulfillmentStatuses {
			if from == to {
				continue
			}
			want := slices.Contains(from.Next(), to)
			if got := triggerAllows(t, from, to); got != want {
				t.Errorf("%s to %s: trigger allows %t, Next lists %t", from, to, got, want)
			}
		}
	}
}

func triggerAllows(t *testing.T, from, to order.FulfillmentStatus) bool {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// The suite's own fixtures hold the shipping method an order needs, and the
	// order lives and dies with this transaction.
	if _, err = tx.Exec(ctx, fixtures); err != nil {
		t.Fatalf("load fixtures: %v", err)
	}
	var orderID uuid.UUID
	if err = tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code, shipping_method_name)
		VALUES (next_order_number(), 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府')
		RETURNING id`).Scan(&orderID); err != nil {
		t.Fatalf("create order: %v", err)
	}

	// Triggers off only to stand the order at 'from' without walking there.
	if _, err = tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatalf("disable triggers: %v", err)
	}
	if _, err = tx.Exec(ctx, `
		UPDATE orders SET fulfillment_status = $2,
		       cancelled_at = CASE WHEN $2 = 'cancelled' THEN now() END,
		       completed_at = CASE WHEN $2 = 'completed' THEN now() END
		WHERE id = $1`, orderID, string(from)); err != nil {
		t.Fatalf("stand the order at %s: %v", from, err)
	}
	if _, err = tx.Exec(ctx, `SET LOCAL session_replication_role = origin`); err != nil {
		t.Fatalf("restore triggers: %v", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE orders SET fulfillment_status = $2,
		       cancelled_at = CASE WHEN $2 = 'cancelled' THEN now() ELSE cancelled_at END,
		       completed_at = CASE WHEN $2 = 'completed' THEN now() ELSE completed_at END
		WHERE id = $1`, orderID, string(to))
	if err == nil {
		return true
	}
	if db.ConstraintName(err) == "" {
		t.Fatalf("%s to %s: %v", from, to, err)
	}
	return db.ConstraintName(err) != "orders_legal_transition"
}
