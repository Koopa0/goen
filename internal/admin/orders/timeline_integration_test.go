//go:build integration

package orders_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/invoicing"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ordernotice"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

// TestTheOrderTimelineMergesEverySource builds one order through each source
// the back office lists and reads them back as one list, oldest first. The
// mail is queued with the producers' own payload types, so a renamed JSON
// field drops it from the list.
func TestTheOrderTimelineMergesEverySource(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	q := db.New(pool)
	number := admintest.PlaceUnpaidOrder(t, pool)
	var orderID uuid.UUID
	var owed int64
	if err := pool.QueryRow(ctx, `SELECT id, order_amount_after_credit(id) FROM orders WHERE order_number = $1`,
		number).Scan(&orderID, &owed); err != nil {
		t.Fatalf("read order %s: %v", number, err)
	}

	// Checkout writes the event and its mail in one transaction, at one now().
	tx, beginErr := pool.Begin(ctx)
	if beginErr != nil {
		t.Fatalf("begin checkout: %v", beginErr)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := q.WithTx(tx).RecordPlacedEvent(ctx, orderID); err != nil {
		t.Fatalf("record placed: %v", err)
	}
	if err := outbox.Enqueue(ctx, q.WithTx(tx), outbox.TopicOrderPlaced, number,
		&email.OrderPlaced{OrderNumber: number, Email: "x@example.com"}); err != nil {
		t.Fatalf("queue the order confirmation: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit checkout: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE outbox_messages SET delivered_at = now() WHERE topic = $1 AND dedupe_key = $2`,
		outbox.TopicOrderPlaced.Name(), number); err != nil {
		t.Fatalf("deliver the order confirmation: %v", err)
	}
	other := admintest.PlaceUnpaidOrder(t, pool)
	if err := outbox.Enqueue(ctx, q, outbox.TopicOrderPlaced, other,
		&email.OrderPlaced{OrderNumber: other, Email: "x@example.com"}); err != nil {
		t.Fatalf("queue another order's confirmation: %v", err)
	}

	// The webhook records the provider's event, the capture, the paid event, its
	// mail and the invoice it makes due, all in one transaction.
	session := "cs_timeline_" + orderID.String()
	payments := payment.NewStore(pool)
	if err := payments.OpenPayment(ctx, number, session, owed); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := payments.ProcessWebhook(ctx, &payment.WebhookEvent{
		ID: "evt_timeline_" + orderID.String(), Type: "checkout.session.completed",
		ObjectRef: session, Payload: []byte(`{"object":"event"}`),
	}, func(ctx context.Context, webhook *payment.WebhookTx) error {
		_, captureErr := webhook.Capture(ctx, payment.Capture{SessionID: session, AmountRecv: owed, Currency: payment.Currency})
		return captureErr
	}); err != nil {
		t.Fatalf("capture through the webhook: %v", err)
	}

	if _, err := s.Advance(ctx, number, order.FulfillmentPicking, uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
		t.Fatalf("staff move the order to picking: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		WITH invoice AS (
			INSERT INTO invoice_documents (order_id, kind, number, amount_cents)
			VALUES ($1, 'invoice', 'GD-' || substr(replace(gen_random_uuid()::text, '-', ''), 1, 8), 100000)
			RETURNING id, order_id, number)
		INSERT INTO invoice_operations
		    (order_id, kind, target_document_id, provider_key, amount_cents, request_payload,
		     actor_user_id, actor_id_snapshot, request_id, send_attempts, last_send_at)
		SELECT order_id, 'allowance', id, number, 50000, '{}', $2, $2, $3, 1, now()
		FROM invoice`, orderID, staff, "timeline-allowance:"+number); err != nil {
		t.Fatalf("send an online allowance: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_operations
		    (order_id, kind, target_document_id, provider_key, amount_cents, request_payload,
		     actor_kind, request_id)
		SELECT order_id, 'void', id, number, amount_cents, '{}', 'system', $2
		FROM invoice_documents WHERE order_id = $1`, orderID, "timeline-void:"+number); err != nil {
		t.Fatalf("claim a system void: %v", err)
	}

	if err := outbox.Enqueue(ctx, q, outbox.TopicOrderShipped, "tcat:"+number,
		&email.OrderShipped{OrderNumber: number, Email: "x@example.com"}); err != nil {
		t.Fatalf("queue the dispatch mail: %v", err)
	}
	if err := ordernotice.Enqueue(ctx, q, &email.OrderTerminal{OrderID: orderID, Kind: email.TerminalDelivered}); err != nil {
		t.Fatalf("queue the delivery notice: %v", err)
	}

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order(%s): %v", number, err)
	}
	type entry struct {
		Label, Status i18n.Key
		ActorKind     admin.ActorKind
		Actor         string
	}
	got := make([]entry, 0, len(view.Timeline))
	for _, e := range view.Timeline {
		got = append(got, entry{Label: e.Label, Status: e.Status, ActorKind: e.ActorKind, Actor: e.Actor})
	}
	want := []entry{
		{Label: i18n.KeyStatusPlaced, ActorKind: admin.ActorCustomer},
		{Label: i18n.KeyAdminTimelineMailPlaced, Status: i18n.KeyAdminTimelineMailSent, ActorKind: admin.ActorSystem},
		{Label: i18n.KeyAdminTimelineProvider, ActorKind: admin.ActorProvider},
		{Label: i18n.KeyStatusPaid, ActorKind: admin.ActorProvider},
		{Label: i18n.KeyAdminTimelineMailPaid, Status: i18n.KeyAdminTimelineMailQueued, ActorKind: admin.ActorSystem},
		{Label: i18n.KeyStatusPicking, ActorKind: admin.ActorStaff, Actor: "稽核測試"},
		{Label: i18n.KeyAuditInvoiceAllowance, Status: i18n.KeyAdminTimelineInvoiceAwaitingBuyer,
			ActorKind: admin.ActorStaff, Actor: "稽核測試"},
		// The store has no invoice writer, so nothing will send the void.
		{Label: i18n.KeyAuditInvoiceVoid, Status: i18n.KeyAdminTimelineInvoiceNotSent, ActorKind: admin.ActorSystem},
		{Label: i18n.KeyAdminTimelineMailShipped, Status: i18n.KeyAdminTimelineMailQueued, ActorKind: admin.ActorSystem},
		{Label: i18n.KeyAdminTimelineMailTerminal, Status: i18n.KeyAdminTimelineMailQueued, ActorKind: admin.ActorSystem},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Order(%s).Timeline mismatch (-want +got):\n%s", number, diff)
	}
	enabled, err := admintest.OrderStore(pool, admintest.Refunder{}, nil, admintest.DisabledInvoiceWriter{}).Order(ctx, number)
	if err != nil {
		t.Fatalf("Order(%s) with e-invoicing on: %v", number, err)
	}
	for _, e := range enabled.Timeline {
		if e.Label == i18n.KeyAuditInvoiceVoid && e.Status != i18n.KeyAdminTimelineInvoicePending {
			t.Errorf("with e-invoicing on, the unsent void reads %q, want %q", e.Status, i18n.KeyAdminTimelineInvoicePending)
		}
	}
	for _, e := range view.Timeline {
		if e.Label == i18n.KeyAdminTimelineProvider && e.Note != "checkout.session.completed" {
			t.Errorf("the provider's event reads %q, want its type checkout.session.completed", e.Note)
		}
	}
}

// A mail and an invoice operation are placed at the moment they were created
// and carry the moment they were delivered or completed, so the list does not
// show a finished state at the time of creation.
func TestTheOrderTimelineCarriesWhenAMailWasDeliveredAndAnOperationCompleted(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	number := admintest.PlaceUnpaidOrder(t, pool)
	if err := outbox.Enqueue(ctx, db.New(pool), outbox.TopicOrderPaid, number,
		&email.OrderPaid{OrderNumber: number, Email: "x@example.com"}); err != nil {
		t.Fatalf("queue the payment mail: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE outbox_messages
		SET created_at = now() - interval '2 hours', delivered_at = now() - interval '1 hour'
		WHERE topic = $1 AND dedupe_key = $2`, outbox.TopicOrderPaid.Name(), number); err != nil {
		t.Fatalf("deliver the payment mail an hour after it was queued: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_operations
		    (order_id, kind, provider_key, amount_cents, request_payload, actor_kind, request_id,
		     status, created_at, completed_at)
		SELECT id, 'issue', replace(order_number, '-', ''), 100000, '{}', 'system', $2,
		       'succeeded', now() - interval '3 hours', now() - interval '2 hours'
		FROM orders WHERE order_number = $1`, number, "timeline-complete:"+number); err != nil {
		t.Fatalf("complete an invoice issue two hours after it was claimed: %v", err)
	}

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order(%s): %v", number, err)
	}
	var issues int
	for _, e := range view.Timeline {
		if e.Label != i18n.KeyAuditInvoiceIssue {
			continue
		}
		issues++
		if e.Status != i18n.KeyAdminTimelineInvoiceSucceeded || e.At == "" || e.DoneAt == "" || e.At == e.DoneAt {
			t.Errorf("the invoice issue reads status %q created %q completed %q, want succeeded with two different times",
				e.Status, e.At, e.DoneAt)
		}
	}
	if issues != 1 {
		t.Errorf("Order(%s).Timeline lists %d invoice issues, want 1", number, issues)
	}
	for _, e := range view.Timeline {
		if e.Label != i18n.KeyAdminTimelineMailPaid {
			continue
		}
		if e.Status != i18n.KeyAdminTimelineMailSent || e.At == "" || e.DoneAt == "" || e.At == e.DoneAt {
			t.Errorf("the payment mail reads status %q created %q delivered %q, want sent with two different times",
				e.Status, e.At, e.DoneAt)
		}
		return
	}
	t.Errorf("Order(%s).Timeline has no payment mail among %d entries", number, len(view.Timeline))
}

// With no 加值中心 the issue a paid sale owes is claimed and waits, as the
// store's ClaimDue intends; the page must not call that work in progress.
func TestAnIssueNoProviderWillSendIsNotShownAsInProgress(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, true)
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_preferences (order_id, invoice_type, customer_name, customer_email)
		VALUES ($1, 'member_carrier', '買受人', 'buyer@example.com')`, orderID); err != nil {
		t.Fatalf("record invoice preference: %v", err)
	}
	if err := invoice.NewStore(pool, &invoice.Gateway{}).ClaimDue(ctx,
		&outbox.InvoiceDue{OrderNumber: number, Trigger: "evt_" + number}); err != nil {
		t.Fatalf("claim the invoice due: %v", err)
	}

	for _, tc := range []struct {
		name   string
		writer invoicing.Writer
		want   i18n.Key
	}{
		{"e-invoicing off", nil, i18n.KeyAdminTimelineInvoiceNotSent},
		{"e-invoicing on", admintest.DisabledInvoiceWriter{}, i18n.KeyAdminTimelineInvoicePending},
	} {
		view, err := admintest.OrderStore(pool, admintest.Refunder{}, nil, tc.writer).Order(ctx, number)
		if err != nil {
			t.Fatalf("%s: read the order: %v", tc.name, err)
		}
		var got []i18n.Key
		for _, e := range view.Timeline {
			if e.Label == i18n.KeyAuditInvoiceIssue {
				got = append(got, e.Status)
			}
		}
		if diff := cmp.Diff([]i18n.Key{tc.want}, got); diff != "" {
			t.Errorf("%s: statuses of the invoice issue entries (-want +got):\n%s", tc.name, diff)
		}
	}
}
