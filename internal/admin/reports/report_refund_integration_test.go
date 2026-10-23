//go:build integration

package reports_test

import (
	"testing"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/reports"
	"github.com/koopa0/goen/internal/admin/stock"
)

// TestStore_ReportAtRefundFigure: the refund figure counts credit too.
func TestStore_ReportAtRefundFigure(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := admintest.ReturnDesk(pool, admintest.Refunder{})
	figures := reports.NewStore(pool, stock.NewStore(pool))

	before, err := figures.ReportAt(ctx, 30, admintest.DatabaseNow(t, pool))
	if err != nil {
		t.Fatalf("read report before refund: %v", err)
	}
	requestID, orderNumber, _ := admintest.CreditFundedReturn(t, pool, 2, 200000)
	if decideErr := s.Decide(ctx, requestID.String(), "approved", "全額購物金", ""); decideErr != nil {
		t.Fatalf("Decide: %v", decideErr)
	}
	after, err := figures.ReportAt(ctx, 30, admintest.DatabaseNow(t, pool))
	if err != nil {
		t.Fatalf("read report after refund: %v", err)
	}

	var card, credit int64
	if err := pool.QueryRow(ctx, `
		SELECT card_cents, credit_cents FROM order_refunds
		WHERE order_number = $1`, orderNumber).Scan(&card, &credit); err != nil {
		t.Fatalf("read the one refund definition: %v", err)
	}
	if card != 0 || credit != 200000 {
		t.Fatalf("order_refunds reads card=%d credit=%d, want 0/200000 — the fixture must be credit-only", card, credit)
	}
	if delta, want := after.RefundedCents-before.RefundedCents, card+credit; delta != want {
		t.Errorf("the report moved by %d while order_refunds says %d went back "+
			"(card %d + credit %d); both surfaces must read one definition", delta, want, card, credit)
	}
}
