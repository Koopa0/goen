//go:build integration

package payment_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/invoice/issuer"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
)

// TestWebhookTx_CaptureQueuesInvoice: A capture queues the invoice it owes
// once. 營業稅法 §32 invoices a prepaid sale when the money arrives, so the
// capture's own transaction queues the issue and the system claims it under the
// event that captured the money. A second delivery of that event, and a
// replayed claim, add nothing.
func TestWebhookTx_CaptureQueuesInvoice(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, orderID := order(t, 129900)
	preferMemberInvoice(t, orderID)
	session := "cs_invoice_due_" + number
	if err := s.OpenPayment(ctx, number, session, 129900); err != nil {
		t.Fatalf("open: %v", err)
	}

	eventID := "evt_invoice_due_" + uuid.NewString()
	deliver := func() (claimed bool) {
		t.Helper()
		claimed, err := s.ProcessWebhook(ctx, &payment.WebhookEvent{
			ID: eventID, Type: "checkout.session.completed", ObjectRef: session,
			Payload: []byte(`{"object":"event"}`),
		}, func(ctx context.Context, tx *payment.WebhookTx) error {
			_, captureErr := tx.Capture(ctx, payment.Capture{
				SessionID: session, AmountRecv: 129900, Currency: payment.Currency,
			})
			return captureErr
		})
		if err != nil {
			t.Fatalf("deliver %s: %v", eventID, err)
		}
		return claimed
	}
	if !deliver() {
		t.Fatal("the first delivery was not claimed")
	}
	due := invoiceDue(t, number)
	if due.Trigger != eventID {
		t.Errorf("queued trigger = %q, want the capturing event %q", due.Trigger, eventID)
	}
	claimDue(t, due)
	assertSystemIssue(t, number, eventID)

	if deliver() {
		t.Fatal("the redelivered event was claimed again")
	}
	claimDue(t, invoiceDue(t, number))
	assertSystemIssue(t, number, eventID)
}

// TestCompleteFundingQueuesNoInvoice: Picking a credit funded order queues no
// invoice. The checkout that spent the credit queued it, so picking, where such
// an order commits, adds none.
func TestCompleteFundingQueuesNoInvoice(t *testing.T) {
	const cents = int64(50000)
	ctx := t.Context()
	userID := creditedUser(t, cents)
	number, orderID := ownedOrder(t, userID, cents)
	spendCreditOnOrder(t, orderID, -cents)

	staff, actor := staffContext(t)
	backOffice := admintest.OrderStore(adminRolePool(t), disabledGateway(t), nil, nil)
	if _, err := backOffice.Advance(staff, number, "picking", uuid.NullUUID{UUID: actor, Valid: true}); err != nil {
		t.Fatalf("admin-role advance to picking: %v", err)
	}
	var queued int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox_messages WHERE topic = $1 AND dedupe_key = $2`,
		outbox.TopicInvoiceDue.Name(), number).Scan(&queued); err != nil {
		t.Fatalf("count invoice.due: %v", err)
	}
	if queued != 0 {
		t.Errorf("picking queued %d invoice.due messages, want 0", queued)
	}
}

// TestUninvoicedOrdersAmountFiled: The health page states the amount the issuer
// files. The amount /admin/health lists for an uninvoiced order is the whole
// NT$ the issue claim then files. Nothing in the shop writes a total with cents
// today, but the schema allows one, and on it the health query once stated the
// raw sum while the issuer floored it (#1933).
func TestUninvoicedOrdersAmountFiled(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	const total, filed = int64(159950), int64(159900)
	number, id := order(t, total)
	preferMemberInvoice(t, id)
	session := "cs_fraction_" + number
	if err := s.OpenPayment(ctx, number, session, total); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := captureThroughWebhook(t, s, payment.Capture{SessionID: session, AmountRecv: total}); err != nil {
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

// TestUninvoicedOrdersLostDue: A paid order whose invoice due was lost is on
// the health page. /admin/health reads the orders, not the outbox that should
// have claimed their invoices, so a paid order whose invoice.due is gone is
// listed until an issue exists. Paid covers a capture and a pending order store
// credit paid in full at checkout; an order still owing is not paid.
func TestUninvoicedOrdersLostDue(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	captured, capturedID := order(t, 159900)
	preferMemberInvoice(t, capturedID)
	session := "cs_uninvoiced_" + captured
	if err := s.OpenPayment(ctx, captured, session, 159900); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := captureThroughWebhook(t, s, payment.Capture{SessionID: session, AmountRecv: 159900}); err != nil {
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
	creditPaid, creditPaidID := ownedOrder(t, creditedUser(t, credit), credit)
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

// invoiceDue is the one invoice.due message queued for number.
func invoiceDue(t *testing.T, number string) outbox.InvoiceDue {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT payload FROM outbox_messages WHERE topic = $1 AND dedupe_key = $2`,
		outbox.TopicInvoiceDue.Name(), number)
	if err != nil {
		t.Fatalf("read invoice.due for %s: %v", number, err)
	}
	payloads, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		t.Fatalf("collect invoice.due for %s: %v", number, err)
	}
	if len(payloads) != 1 {
		t.Fatalf("%d invoice.due messages for %s, want 1", len(payloads), number)
	}
	var due outbox.InvoiceDue
	if err := json.Unmarshal(payloads[0], &due); err != nil {
		t.Fatalf("decode invoice.due: %v", err)
	}
	if due.OrderNumber != number {
		t.Fatalf("invoice.due names order %q, want %q", due.OrderNumber, number)
	}
	return due
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

// assertSystemIssue holds number to exactly one issue operation, claimed by the
// system: no user, and trigger as its request id.
func assertSystemIssue(t *testing.T, number, trigger string) {
	t.Helper()
	var issues int
	var kind, requestID string
	var actor, snapshot uuid.NullUUID
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) OVER (), op.actor_kind, op.actor_user_id, op.actor_id_snapshot,
		       op.request_id
		FROM invoice_operations op JOIN orders o ON o.id = op.order_id
		WHERE o.order_number = $1 AND op.kind = 'issue'
		ORDER BY op.created_at LIMIT 1`, number).
		Scan(&issues, &kind, &actor, &snapshot, &requestID); err != nil {
		t.Fatalf("read the issue of %s: %v", number, err)
	}
	if issues != 1 {
		t.Errorf("%d issue operations for %s, want 1", issues, number)
	}
	if kind != "system" || actor.Valid || snapshot.Valid {
		t.Errorf("issue actor = %s user=%v snapshot=%v, want system with no user",
			kind, actor, snapshot)
	}
	if requestID != trigger {
		t.Errorf("issue request id = %q, want %q", requestID, trigger)
	}
}
