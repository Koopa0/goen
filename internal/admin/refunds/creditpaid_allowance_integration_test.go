//go:build integration

package refunds_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/invoice/issuer"
	"github.com/koopa0/goen/internal/outbox"
)

// TestStore_RefundBeforeShipmentCreditPaid: a credit paid order cancelled past
// the void window can still be allowed. Store credit paid the order, checkout
// queued its 統一發票, and staff cancel it after the 13th of the following odd
// month. ECPay no longer voids the invoice, so only an allowance can relieve it,
// and an allowance may relieve no more than has gone back to the customer. The
// credit the cancellation gave back is that money: the order page offers the
// whole invoice, filing it succeeds, and once the buyer agrees the invoice
// leaves the health page.
func TestStore_RefundBeforeShipmentCreditPaid(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	fake, invoices := fakeECPay(t, time.Now().AddDate(0, -4, 0))
	s := refunds.NewStore(pool, admintest.HealthyGateway(t), invoices)
	number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 0, 500000, false)
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_preferences (order_id, invoice_type, customer_name, customer_email)
		VALUES ($1, 'member_carrier', '買受人', 'buyer@example.com')`, orderID); err != nil {
		t.Fatalf("record invoice preference: %v", err)
	}
	if err := invoices.ClaimDue(ctx, &outbox.InvoiceDue{OrderNumber: number, Trigger: "evt_" + number}); err != nil {
		t.Fatalf("claim the invoice due: %v", err)
	}
	issueNow(t, ctx, invoices, orderID)

	if got := pressRefund(t, ctx, s, number); got != "/admin/orders/"+number+"?refunded=1" {
		t.Fatalf("press redirected to %s, want the credit-paid order cancelled", got)
	}
	if got := admintest.FulfillmentOf(t, pool, orderID); got != "cancelled" {
		t.Fatalf("order is %s, want cancelled", got)
	}
	if got := fake.Calls("/B2CInvoice/Invalid"); got != 0 {
		t.Fatalf("%d Invalid calls past ECPay's void window, want none", got)
	}

	var card, credit int64
	if err := pool.QueryRow(ctx,
		`SELECT card_cents, credit_cents FROM order_refunds WHERE order_id = $1`, orderID).Scan(&card, &credit); err != nil {
		t.Fatalf("read what went back: %v", err)
	}
	if card != 0 || credit != 500000 {
		t.Fatalf("order_refunds reads card %d, credit %d; want the 500000 reversed spend as credit", card, credit)
	}

	store := admintest.OrderStore(pool, admintest.HealthyGateway(t), invoices)
	view, err := store.Order(ctx, number)
	if err != nil {
		t.Fatalf("fill the order page: %v", err)
	}
	if view.RefundedCents != 500000 || !view.CanAllowInvoice() {
		t.Fatalf("order page shows %d refunded and allowance offered = %v; want 500000 and the form",
			view.RefundedCents, view.CanAllowInvoice())
	}
	if !listedOnHealth(t, number) {
		t.Fatalf("the live invoice of cancelled %s is not on the health page", number)
	}
	// One door for one correction: the credit given back is owed an allowance
	// too, but a cancelled order's invoice is listed as such and only there.
	unrelieved, _, err := health.NewStore(pool).UnrelievedRefunds(ctx, 0)
	if err != nil {
		t.Fatalf("read the health page's refunds with no allowance: %v", err)
	}
	for _, r := range unrelieved {
		if r.OrderNumber == number {
			t.Errorf("cancelled %s is also listed as a refund with no allowance", number)
		}
	}

	// The staff member's own press, through the same door the form posts to.
	if err := store.FileAllowance(ctx, number, uuid.New()); err != nil && !errors.Is(err, issuer.ErrAwaitingBuyer) {
		t.Fatalf("allow the whole invoice of %s: %v", number, err)
	}
	var allowance struct {
		id            uuid.UUID
		status        string
		sends, amount int64
		actor         uuid.UUID
	}
	// No card refund exists here, which allowanceOf's ordering check needs.
	if err := pool.QueryRow(ctx, `
		SELECT op.id, op.status, op.send_attempts, op.amount_cents, op.actor_user_id
		FROM invoice_operations op WHERE op.order_id = $1 AND op.kind = 'allowance'`,
		orderID).Scan(&allowance.id, &allowance.status, &allowance.sends, &allowance.amount, &allowance.actor); err != nil {
		t.Fatalf("read the allowance: %v", err)
	}
	if allowance.status != "pending" || allowance.sends != 1 || allowance.amount != 500000 || allowance.actor != staff {
		t.Errorf("allowance %+v, want pending, sent once, for the whole 500000, by the staff member", allowance)
	}
	if got := fake.Calls("/B2CInvoice/AllowanceByCollegiate"); got != 1 {
		t.Errorf("%d allowances asked of ECPay, want one", got)
	}

	fake.BuyerAgrees()
	dueNow(t, ctx, allowance.id)
	if err := invoices.ProcessOperation(ctx, allowance.id); err != nil {
		t.Fatalf("settle the agreed allowance: %v", err)
	}
	if listedOnHealth(t, number) {
		t.Errorf("cancelled %s is still on the health page after its allowance settled", number)
	}
}

func listedOnHealth(t *testing.T, number string) bool {
	t.Helper()
	listed, _, err := health.NewStore(pool).CancelledOrderInvoices(t.Context(), 0)
	if err != nil {
		t.Fatalf("read the health page's list: %v", err)
	}
	for _, inv := range listed {
		if inv.OrderNumber == number {
			return true
		}
	}
	return false
}
