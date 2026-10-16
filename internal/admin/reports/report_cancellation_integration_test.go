//go:build integration

package reports_test

import (
	"testing"
	"time"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/reports"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/order/buyer"
)

// TestCancellingACreditPaidOrderMovesNoReportFigure holds the owner's ruling on
// #80: the credit an order gave back when it was cancelled is not a refund of
// revenue, because that order was never revenue. order_refunds does count it (an
// allowance may relieve it), so the report must leave it out itself, and the
// refund figure must not move when the cancellation writes the reversal.
func TestCancellingACreditPaidOrderMovesNoReportFigure(t *testing.T) {
	ctx := t.Context()
	s := reports.NewStore(pool, stock.NewStore(pool))
	number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 0, 500000, false)
	before, err := s.ReportAt(ctx, 7, time.Now())
	if err != nil {
		t.Fatalf("report before: %v", err)
	}

	if err = buyer.NewStore(pool).CancelOrder(ctx, number); err != nil {
		t.Fatalf("cancel %s: %v", number, err)
	}

	// The premise: the cancellation gave the whole spend back, on the order.
	var credit int64
	if err = pool.QueryRow(ctx,
		`SELECT credit_cents FROM order_refunds WHERE order_id = $1`, orderID).Scan(&credit); err != nil {
		t.Fatalf("read what went back: %v", err)
	}
	if credit != 500000 {
		t.Fatalf("order_refunds reads %d credit after the cancellation, want the 500000 spend back", credit)
	}

	// Read at a later moment: the reversal is stamped by the database when the
	// cancellation commits, and a report reads what landed by its own now.
	after, err := s.ReportAt(ctx, 7, time.Now())
	if err != nil {
		t.Fatalf("report after: %v", err)
	}
	if after.RefundedCents != before.RefundedCents || after.Previous.RefundedCents != before.Previous.RefundedCents {
		t.Errorf("refunded moved from %d to %d (previous period %d to %d) when a never-sold order's credit went back",
			before.RefundedCents, after.RefundedCents, before.Previous.RefundedCents, after.Previous.RefundedCents)
	}
	if after.RevenueCents != before.RevenueCents || after.Orders != before.Orders {
		t.Errorf("revenue %d over %d orders became %d over %d by cancelling an order that was never sold",
			before.RevenueCents, before.Orders, after.RevenueCents, after.Orders)
	}
}
