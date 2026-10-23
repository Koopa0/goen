//go:build integration

package health_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/invoice/issuer"
	"github.com/koopa0/goen/internal/order/ordertest"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
)

// TestStore_UninvoicedOrdersAmountFiled: The health page states the amount the
// issuer files. The amount /admin/health lists for an uninvoiced order is the
// whole NT$ the issue claim then files. Nothing in the shop writes a total with
// cents today, but the schema allows one, and on it the health query once
// stated the raw sum while the issuer floored it (#1933).
func TestStore_UninvoicedOrdersAmountFiled(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	const total, filed = int64(159950), int64(159900)
	number, id := order(t, total)
	preferMemberInvoice(t, id)
	session := "cs_fraction_" + number
	if err := s.OpenPayment(ctx, number, session, total); err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := captureThroughWebhook(t, s, payment.Capture{SessionID: session, AmountRecv: total}); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if tag, err := pool.Exec(ctx,
		`DELETE FROM outbox_messages WHERE topic = $1 AND dedupe_key = $2`,
		outbox.TopicInvoiceDue.Name(), number); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("delete the queued invoice.due of %s: %d rows, %v", number, tag.RowsAffected(), err)
	}

	orders, _, err := health.NewStore(adminRolePool(t)).UninvoicedOrders(ctx, 0)
	if err != nil {
		t.Fatalf("read paid orders with no invoice operation: %v", err)
	}
	listed := int64(-1)
	for _, o := range orders {
		if o.OrderNumber == number {
			listed = o.AmountCents
		}
	}
	if listed != filed {
		t.Errorf("/admin/health lists %s at %d cents, want the %d the issuer files", number, listed, filed)
	}

	claimDue(t, outbox.InvoiceDue{OrderNumber: number, Trigger: "evt_fraction_" + number})
	var claimed int64
	if err := pool.QueryRow(ctx,
		`SELECT amount_cents FROM invoice_operations WHERE order_id = $1 AND kind = 'issue'`,
		id).Scan(&claimed); err != nil {
		t.Fatalf("read the issue claim of %s: %v", number, err)
	}
	if claimed != filed {
		t.Errorf("the issue claim of %s files %d cents, want %d", number, claimed, filed)
	}
}

// TestStore_UninvoicedOrdersLostDue: A paid order whose invoice due was lost is
// on the health page. /admin/health reads the orders, not the outbox that
// should have claimed their invoices, so a paid order whose invoice.due is gone
// is listed until an issue exists. Paid covers a capture and a pending order
// store credit paid in full at checkout; an order still owing is not paid.
func TestStore_UninvoicedOrdersLostDue(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	captured, capturedID := order(t, 159900)
	preferMemberInvoice(t, capturedID)
	session := "cs_uninvoiced_" + captured
	if err := s.OpenPayment(ctx, captured, session, 159900); err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := captureThroughWebhook(t, s, payment.Capture{SessionID: session, AmountRecv: 159900}); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if tag, err := pool.Exec(ctx,
		`DELETE FROM outbox_messages WHERE topic = $1 AND dedupe_key = $2`,
		outbox.TopicInvoiceDue.Name(), captured); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("delete the queued invoice.due of %s: %d rows, %v", captured, tag.RowsAffected(), err)
	}

	// Credit spent outside checkout queues nothing: the message is as lost as
	// a deleted one.
	const credit = int64(50000)
	creditPaid, creditPaidID := ownedOrder(t, ordertest.CreditedCustomer(t, pool, credit), credit)
	preferMemberInvoice(t, creditPaidID)
	spendCreditOnOrder(t, creditPaidID, -credit)
	owing, _ := order(t, 70000)

	backOffice := health.NewStore(adminRolePool(t))
	listed := func(number string) bool {
		t.Helper()
		orders, total, err := backOffice.UninvoicedOrders(ctx, 0)
		if err != nil {
			t.Fatalf("read paid orders with no invoice operation: %v", err)
		}
		if total < int64(len(orders)) {
			t.Errorf("total %d is below the %d orders listed", total, len(orders))
		}
		for _, o := range orders {
			if o.OrderNumber == number {
				return true
			}
		}
		return false
	}
	if !listed(captured) {
		t.Errorf("%s was captured, its invoice.due is gone, and /admin/health does not list it", captured)
	}
	if !listed(creditPaid) {
		t.Errorf("store credit paid all of pending %s, it has no invoice operation, and "+
			"/admin/health does not list it", creditPaid)
	}
	if listed(owing) {
		t.Errorf("%s still owes its whole total and is listed as paid", owing)
	}

	for _, number := range []string{captured, creditPaid} {
		claimDue(t, outbox.InvoiceDue{OrderNumber: number, Trigger: "evt_reissued_" + number})
		if listed(number) {
			t.Errorf("%s is still listed once it holds an issue operation", number)
		}
	}
}

// order is an unpaid order of one line worth cents, for a customer of its own.
func order(t *testing.T, cents int64) (number string, id uuid.UUID) {
	t.Helper()
	return ownedOrder(t, ordertest.Customer(t, pool), cents)
}

// ownedOrder is an unpaid order of one line worth cents, placed by userID.
func ownedOrder(t *testing.T, userID uuid.UUID, cents int64) (number string, id uuid.UUID) {
	t.Helper()
	id = admintest.OrderForCustomer(t, pool, userID, cents, false)
	return ordertest.NumberOf(t, pool, id), id
}

// captureThroughWebhook applies c through the same transaction boundary as a
// real webhook. Every call uses a distinct event because these tests exercise
// capture behavior, not webhook redelivery. A paid session always names its
// currency, so a fixture that leaves it out means the one goen charges in.
func captureThroughWebhook(t *testing.T, s *payment.Store, c payment.Capture) error {
	t.Helper()
	if c.Currency == "" {
		c.Currency = payment.Currency
	}
	eventID := "evt_capture_test_" + uuid.NewString()
	claimed, err := s.ProcessWebhook(t.Context(), &payment.WebhookEvent{
		ID: eventID, Type: "checkout.session.completed", ObjectRef: c.SessionID,
		Payload: []byte(`{"object":"event"}`),
	}, func(ctx context.Context, tx *payment.WebhookTx) error {
		_, captureErr := tx.Capture(ctx, c)
		return captureErr
	})
	if err != nil {
		return err
	}
	if !claimed {
		return fmt.Errorf("unique capture webhook %s was not claimed", eventID)
	}
	return nil
}

func spendCreditOnOrder(t *testing.T, orderID uuid.UUID, debitCents int64) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `SELECT spend_store_credit($1, $2)`,
		orderID, debitCents); err != nil {
		t.Fatalf("spend credit on the order: %v", err)
	}
}

// preferMemberInvoice records the checkout's 發票 choice, the filing snapshot a
// claim is built from.
func preferMemberInvoice(t *testing.T, orderID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO invoice_preferences (order_id, invoice_type, customer_name, customer_email)
		VALUES ($1, 'member_carrier', '收件', 'pay@example.com')`, orderID); err != nil {
		t.Fatalf("record the 發票 choice: %v", err)
	}
}

// claimDue runs the invoice.due handler as the worker does, on the admin role:
// the outbox drains on the store pool, which holds no EXECUTE on the claim.
func claimDue(t *testing.T, due outbox.InvoiceDue) {
	t.Helper()
	gateway, err := issuer.NewGateway("", "", "", "")
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	if err := issuer.NewStore(adminRolePool(t), gateway).ClaimDue(t.Context(), &due); err != nil {
		t.Fatalf("claim the invoice %s owes: %v", due.OrderNumber, err)
	}
}

// adminRolePool connects as the back office's role, which holds the claim
// door's EXECUTE; the suite's own pool owns everything and would hide a missing
// grant.
func adminRolePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse admin-role pool: %v", err)
	}
	cfg.MaxConns = 1
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, roleErr := conn.Exec(ctx, "SET ROLE admin")
		return roleErr
	}
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open admin-role pool: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}
