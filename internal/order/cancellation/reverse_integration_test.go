//go:build integration

package cancellation_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/checkout/checkouttest"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// TestReverseOrderLedgerIdempotent: reverse_order_credit returns a cancelled
// order's credit on the first call and nothing on the next two.
func TestReverseOrderLedgerIdempotent(t *testing.T) {
	ctx := t.Context()
	userID, accountID := checkouttest.CreditedAccount(t, pool, 50000)
	number := ordertest.PendingOrderSpendingCredit(t, pool, userID, 20000)

	var orderID uuid.UUID
	if err := pool.QueryRow(ctx,
		`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
		 WHERE order_number = $1 RETURNING id`, number).Scan(&orderID); err != nil {
		t.Fatalf("cancel the order: %v", err)
	}

	for i := range 3 {
		var returned int64
		if err := pool.QueryRow(ctx,
			`SELECT reverse_order_credit($1)`, orderID).Scan(&returned); err != nil {
			t.Fatalf("reverse %d: %v", i+1, err)
		}
		want := int64(20000)
		if i > 0 {
			want = 0
		}
		if returned != want {
			t.Errorf("call %d returned %d cents, want %d", i+1, returned, want)
		}
	}
	if got := checkouttest.CreditBalance(t, pool, accountID); got != 50000 {
		t.Errorf("balance after three reversals = %d, want 50000", got)
	}
}

// TestReverseOrderLedgerShipped: reverse_order_credit refuses a shipped order's
// credit with store_credit_posting_matches_order and pays nothing.
func TestReverseOrderLedgerShipped(t *testing.T) {
	ctx := t.Context()
	userID, accountID := checkouttest.CreditedAccount(t, pool, 50000)
	number := ordertest.PendingOrderSpendingCredit(t, pool, userID, 20000)

	// Through the real transitions: orders_check_transition refuses pending →
	// shipped, because an order is picked before it leaves.
	var orderID uuid.UUID
	for _, status := range []string{"picking", "shipped"} {
		if err := pool.QueryRow(ctx,
			`UPDATE orders SET fulfillment_status = $2 WHERE order_number = $1
			 RETURNING id`, number, status).Scan(&orderID); err != nil {
			t.Fatalf("move the order to %s: %v", status, err)
		}
	}

	var returned int64
	err := pool.QueryRow(ctx, `SELECT reverse_order_credit($1)`, orderID).Scan(&returned)
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "store_credit_posting_matches_order" {
		t.Errorf("reversing a shipped order's credit = %v, want "+
			"store_credit_posting_matches_order", err)
	}
	if got := checkouttest.CreditBalance(t, pool, accountID); got != 30000 {
		t.Errorf("balance = %d, want 30000 — the refusal must not have paid anything", got)
	}
}
