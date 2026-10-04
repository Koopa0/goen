//go:build integration

package orders_test

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/ui/pages"
)

// The trigger's legality check raises before every precondition (payment,
// refund, invoice, parcels), so a refusal under any other constraint means the
// transition table itself let the pair through.
func TestTheTriggerAcceptsTheTransitionsTheDeskOffers(t *testing.T) {
	customer := admintest.CreditedAccount(t, pool, 0)
	orderID := admintest.OrderForCustomer(t, pool, customer, 1000, false)

	// picking to shipped is the dispatch itself: the desk does not offer it as a
	// status change because only Store.Ship also settles the stock.
	dispatch := [2]pages.FulfillmentStatus{pages.FulfillmentPicking, pages.FulfillmentShipped}

	for _, from := range pages.FulfillmentStatuses {
		for _, to := range pages.FulfillmentStatuses {
			if from == to {
				continue
			}
			want := slices.Contains(orders.NextStatuses(from), to) || [2]pages.FulfillmentStatus{from, to} == dispatch
			if got := triggerAllows(t, orderID, from, to); got != want {
				t.Errorf("%s to %s: trigger allows %t, the desk offers %t", from, to, got, want)
			}
		}
	}
}

func triggerAllows(t *testing.T, orderID uuid.UUID, from, to pages.FulfillmentStatus) bool {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

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
	if admintest.ConstraintName(err) == "" {
		t.Fatalf("%s to %s: %v", from, to, err)
	}
	return admintest.ConstraintName(err) != "orders_legal_transition"
}
