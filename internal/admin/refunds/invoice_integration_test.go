//go:build integration

package refunds_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/admin/invoicing"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

func fakeECPay(t *testing.T, issuedAt time.Time) (*invoice.FakeECPay, *invoice.Store) {
	t.Helper()
	fake, err := invoice.NewFakeECPay(issuedAt)
	if err != nil {
		t.Fatalf("fake ECPay: %v", err)
	}
	t.Cleanup(fake.Close)
	return fake, invoice.NewStore(pool, fake.Gateway())
}

// capturedOrderOwingAnInvoice is a card-captured, unshipped order whose
// 統一發票 the system has claimed and not yet sent.
func capturedOrderOwingAnInvoice(t *testing.T, ctx context.Context, invoices *invoice.Store) (string, uuid.UUID) {
	t.Helper()
	number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, true)
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_preferences (order_id, invoice_type, customer_name, customer_email)
		VALUES ($1, 'member_carrier', '買受人', 'buyer@example.com')`, orderID); err != nil {
		t.Fatalf("record invoice preference: %v", err)
	}
	if err := invoices.ClaimDue(ctx, &outbox.InvoiceDue{OrderNumber: number, Trigger: "evt_" + number}); err != nil {
		t.Fatalf("claim the invoice due: %v", err)
	}
	return number, orderID
}

func issueNow(t *testing.T, ctx context.Context, invoices *invoice.Store, orderID uuid.UUID) {
	t.Helper()
	var operationID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT id FROM invoice_operations WHERE order_id = $1 AND kind = 'issue'`,
		orderID).Scan(&operationID); err != nil {
		t.Fatalf("read the system issue: %v", err)
	}
	if err := invoices.ProcessOperation(ctx, operationID); err != nil {
		t.Fatalf("issue the system claim: %v", err)
	}
}

func pressRefund(t *testing.T, ctx context.Context, s *refunds.Store, number string) string {
	t.Helper()
	h := refunds.NewHandler(s, nil, slog.New(slog.DiscardHandler))
	form := url.Values{"confirm": {"refund"}, "total": {"500000"}, "reason": {"顧客來電要求取消"}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/orders/"+number+"/refund", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("number", number)
	rec := httptest.NewRecorder()
	h.RefundBeforeShipment(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("refund before shipment = %d, want 303", rec.Code)
	}
	return rec.Header().Get("Location")
}

// TestRefundBeforeShipmentVoidsTheInvoiceBeforeThePayout: inside ECPay's void
// window the staff member's press voids the system's invoice, under their own
// name, before any money moves, and cancels the order in the same press.
func TestRefundBeforeShipmentVoidsTheInvoiceBeforeThePayout(t *testing.T) {
	for _, inFlight := range []bool{false, true} {
		name := "issued"
		if inFlight {
			name = "issue in flight"
		}
		t.Run(name, func(t *testing.T) {
			ctx, staff := admintest.StaffContext(t, pool)
			fake, invoices := fakeECPay(t, time.Now())
			var sent atomic.Int64
			s := refunds.NewStore(pool, admintest.Refunder{Sent: &sent}, invoices)
			number, orderID := capturedOrderOwingAnInvoice(t, ctx, invoices)
			if !inFlight {
				issueNow(t, ctx, invoices, orderID)
			}

			if got := pressRefund(t, ctx, s, number); got != "/admin/orders/"+number+"?refunded=1" {
				t.Fatalf("one press redirected to %s, want the refund done", got)
			}
			if got := admintest.FulfillmentOf(t, pool, orderID); got != "cancelled" || sent.Load() != 1 {
				t.Fatalf("order %s after %d card refunds, want cancelled after one", got, sent.Load())
			}
			var voids int
			var byStaff bool
			var voided, refunded *time.Time
			if err := pool.QueryRow(ctx, `
				SELECT count(*),
				       coalesce(bool_and(op.status = 'succeeded' AND op.actor_kind = 'staff'
				                         AND op.actor_user_id = $2
				                         AND op.request_payload ->> 'reason' = '訂單取消'), false),
				       max(op.completed_at),
				       (SELECT min(f.created_at) FROM refunds f
				        JOIN return_requests r ON r.id = f.return_request_id WHERE r.order_id = $1)
				FROM invoice_operations op WHERE op.order_id = $1 AND op.kind = 'void'`,
				orderID, staff).Scan(&voids, &byStaff, &voided, &refunded); err != nil {
				t.Fatalf("read the void: %v", err)
			}
			if voids != 1 || !byStaff || fake.Calls("/B2CInvoice/Invalid") != 1 {
				t.Fatalf("%d void operations (by staff: %v), %d Invalid calls; want one by the staff member",
					voids, byStaff, fake.Calls("/B2CInvoice/Invalid"))
			}
			if voided == nil || refunded == nil || !voided.Before(*refunded) {
				t.Errorf("void settled at %v, card refund at %v; want the void first", voided, refunded)
			}
			if inFlight && fake.Calls("/B2CInvoice/Issue") != 1 {
				t.Errorf("the issue in flight was sent %d times, want once before its void",
					fake.Calls("/B2CInvoice/Issue"))
			}
		})
	}
}

// TestRefundBeforeShipmentAsksTheBuyerForAnAllowancePastTheVoidWindow: once
// the period is filed no void is asked for; after the refund the buyer is
// e-mailed an allowance for it, which lets the order cancel in the same press,
// and it settles from the list once they agree, keeping goen's copy of that
// agreement.
func TestRefundBeforeShipmentAsksTheBuyerForAnAllowancePastTheVoidWindow(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	fake, invoices := fakeECPay(t, time.Now().AddDate(0, -4, 0))
	var sent atomic.Int64
	s := refunds.NewStore(pool, admintest.Refunder{Sent: &sent}, invoices)
	number, orderID := capturedOrderOwingAnInvoice(t, ctx, invoices)
	issueNow(t, ctx, invoices, orderID)

	if got := pressRefund(t, ctx, s, number); got != "/admin/orders/"+number+"?refunded=1" {
		t.Fatalf("press redirected to %s, want the order cancelled with its allowance sent", got)
	}
	allowance := allowanceOf(t, ctx, orderID)
	if fake.Calls("/B2CInvoice/AllowanceByCollegiate") != 1 || fake.Calls("/B2CInvoice/Invalid") != 0 ||
		sent.Load() != 1 {
		t.Fatalf("%d allowances asked for, %d Invalid calls, %d card refunds; want one allowance and the refund",
			fake.Calls("/B2CInvoice/AllowanceByCollegiate"), fake.Calls("/B2CInvoice/Invalid"), sent.Load())
	}
	if allowance.status != "pending" || allowance.sends != 1 || allowance.amount != 500000 ||
		allowance.actor != staff || !allowance.afterRefund {
		t.Errorf("allowance %+v, want pending, sent once, 500000, by the staff member, after the refund", allowance)
	}
	if got := admintest.FulfillmentOf(t, pool, orderID); got != "cancelled" {
		t.Errorf("order is %s with its allowance sent to the buyer, want cancelled", got)
	}

	fake.BuyerAgrees()
	dueNow(t, ctx, allowance.id)
	if err := invoices.ProcessOperation(ctx, allowance.id); err != nil {
		t.Fatalf("settle the agreed allowance: %v", err)
	}
	var status, ip, email, agreedAt string
	var documents int
	if err := pool.QueryRow(ctx, `
		SELECT op.status, op.buyer_consent ->> 'ip', op.buyer_consent ->> 'email',
		       op.buyer_consent ->> 'agreed_at',
		       (SELECT count(*) FROM invoice_documents d
		        WHERE d.id = op.result_document_id AND d.kind = 'allowance' AND d.amount_cents = 500000)
		FROM invoice_operations op WHERE op.id = $1`, allowance.id).Scan(
		&status, &ip, &email, &agreedAt, &documents); err != nil {
		t.Fatalf("read the settled allowance: %v", err)
	}
	if status != "succeeded" || documents != 1 || ip != invoice.FakeBuyerIP ||
		email != "buyer@example.com" || agreedAt == "" {
		t.Errorf("allowance %s with %d documents, consent ip %q email %q at %q; "+
			"want it settled with the buyer's agreement kept", status, documents, ip, email, agreedAt)
	}
}

// TestAnAllowanceTheBuyerIgnoresNeedsAPerson: 72 hours after the e-mail with
// nothing on the list, the operation needs staff and the order page says why.
func TestAnAllowanceTheBuyerIgnoresNeedsAPerson(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	_, invoices := fakeECPay(t, time.Now().AddDate(0, -4, 0))
	s := refunds.NewStore(pool, admintest.Refunder{}, invoices)
	number, orderID := capturedOrderOwingAnInvoice(t, ctx, invoices)
	issueNow(t, ctx, invoices, orderID)
	if got := pressRefund(t, ctx, s, number); got != "/admin/orders/"+number+"?refunded=1" {
		t.Fatalf("press redirected to %s, want the order cancelled with its allowance sent", got)
	}
	allowance := allowanceOf(t, ctx, orderID)

	if _, err := pool.Exec(ctx, `
		UPDATE invoice_operations SET last_send_at = now() - interval '73 hours'
		WHERE id = $1`, allowance.id); err != nil {
		t.Fatalf("let the link lapse: %v", err)
	}
	dueNow(t, ctx, allowance.id)
	if err := invoices.ProcessOperation(ctx, allowance.id); err == nil {
		t.Fatal("an allowance the buyer never agreed to settled")
	}
	var status, category string
	if err := pool.QueryRow(ctx, `
		SELECT status, coalesce(last_error, '') FROM invoice_operations WHERE id = $1`,
		allowance.id).Scan(&status, &category); err != nil {
		t.Fatalf("read the lapsed allowance: %v", err)
	}
	if status != "attention" || category != invoice.CategoryBuyerUnconfirmed {
		t.Errorf("lapsed allowance is %s/%s, want attention/%s", status, category, invoice.CategoryBuyerUnconfirmed)
	}
	var view admin.OrderView
	if err := invoicing.NewStore(pool, invoices, invoices).FillOrder(ctx, &view, number); err != nil {
		t.Fatalf("fill the order page: %v", err)
	}
	if view.AllowanceAttention != invoice.CategoryBuyerUnconfirmed || view.AllowanceAwaitingUntil != "" {
		t.Errorf("order page shows attention %q, awaiting %q; want only that the customer never agreed",
			view.AllowanceAttention, view.AllowanceAwaitingUntil)
	}
}

type sentAllowance struct {
	id          uuid.UUID
	status      string
	sends       int
	amount      int64
	actor       uuid.UUID
	afterRefund bool
}

func allowanceOf(t *testing.T, ctx context.Context, orderID uuid.UUID) sentAllowance {
	t.Helper()
	var a sentAllowance
	if err := pool.QueryRow(ctx, `
		SELECT op.id, op.status, op.send_attempts, op.amount_cents, op.actor_user_id,
		       op.created_at > (SELECT max(f.created_at) FROM refunds f
		                        JOIN return_requests r ON r.id = f.return_request_id
		                        WHERE r.order_id = $1)
		FROM invoice_operations op WHERE op.order_id = $1 AND op.kind = 'allowance'`,
		orderID).Scan(&a.id, &a.status, &a.sends, &a.amount, &a.actor, &a.afterRefund); err != nil {
		t.Fatalf("read the allowance: %v", err)
	}
	return a
}

func dueNow(t *testing.T, ctx context.Context, operationID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`UPDATE invoice_operations SET available_at = now() WHERE id = $1`, operationID); err != nil {
		t.Fatalf("make operation %s due: %v", operationID, err)
	}
}

// TestRefundBeforeShipmentWithdrawsAnIssueNothingWillSend: with no 加值中心 the
// system's claim would wait for ever and hold the cancellation with it.
func TestRefundBeforeShipmentWithdrawsAnIssueNothingWillSend(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	invoices := invoice.NewStore(pool, &invoice.Gateway{})
	s := refunds.NewStore(pool, admintest.Refunder{}, invoices)
	number, orderID := capturedOrderOwingAnInvoice(t, ctx, invoices)

	if got := pressRefund(t, ctx, s, number); got != "/admin/orders/"+number+"?refunded=1" {
		t.Fatalf("press redirected to %s, want the refund done", got)
	}
	var status, category string
	if err := pool.QueryRow(ctx, `
		SELECT status, coalesce(last_error, '') FROM invoice_operations
		WHERE order_id = $1 AND kind = 'issue'`, orderID).Scan(&status, &category); err != nil {
		t.Fatalf("read the issue: %v", err)
	}
	if status != "rejected" || category != "issue_withdrawn_order_cancelled" {
		t.Errorf("issue is %s (%s), want rejected as withdrawn", status, category)
	}
	if got := admintest.FulfillmentOf(t, pool, orderID); got != "cancelled" {
		t.Errorf("order is %s, want cancelled in one press", got)
	}
}

// TestRefundBeforeShipmentPaysOutWhileECPayHangs: a 加值中心 that stops
// answering takes only its share of the request; the card is refunded and the
// void waits for the reconciler.
func TestRefundBeforeShipmentPaysOutWhileECPayHangs(t *testing.T) {
	staff, _ := admintest.StaffContext(t, pool)
	fake, invoices := fakeECPay(t, time.Now())
	var sent atomic.Int64
	s := refunds.NewStore(pool, admintest.Refunder{Sent: &sent}, invoices)
	number, orderID := capturedOrderOwingAnInvoice(t, staff, invoices)
	issueNow(t, staff, invoices, orderID)
	fake.HoldInvalid()

	ctx, cancel := context.WithTimeout(staff, 6*time.Second)
	defer cancel()
	if got := pressRefund(t, ctx, s, number); got != "/admin/orders/"+number+"?cancelinvoice=1" {
		t.Fatalf("press redirected to %s, want the invoice left pending", got)
	}
	var status string
	if err := pool.QueryRow(staff, `
		SELECT status FROM invoice_operations WHERE order_id = $1 AND kind = 'void'`,
		orderID).Scan(&status); err != nil {
		t.Fatalf("read the void: %v", err)
	}
	if sent.Load() != 1 || status != "pending" {
		t.Errorf("%d card refunds, void %s; want the refund sent and the void pending", sent.Load(), status)
	}
}

// TestRefundBeforeShipmentKeepsAnIssueThatMayHaveReachedECPay: an issue sent
// before the 加值中心 was unconfigured may exist at ECPay, so it is not withdrawn.
func TestRefundBeforeShipmentKeepsAnIssueThatMayHaveReachedECPay(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	invoices := invoice.NewStore(pool, &invoice.Gateway{})
	var sent atomic.Int64
	s := refunds.NewStore(pool, admintest.Refunder{Sent: &sent}, invoices)
	number, orderID := capturedOrderOwingAnInvoice(t, ctx, invoices)
	if _, err := pool.Exec(ctx, `
		UPDATE invoice_operations SET send_attempts = 1, last_send_at = now()
		WHERE order_id = $1 AND kind = 'issue'`, orderID); err != nil {
		t.Fatalf("mark the issue sent: %v", err)
	}

	if got := pressRefund(t, ctx, s, number); got != "/admin/orders/"+number+"?cancelinvoice=1" {
		t.Fatalf("press redirected to %s, want the issue left to staff", got)
	}
	var status string
	if err := pool.QueryRow(ctx, `
		SELECT status FROM invoice_operations WHERE order_id = $1 AND kind = 'issue'`,
		orderID).Scan(&status); err != nil {
		t.Fatalf("read the issue: %v", err)
	}
	if status != "pending" || sent.Load() != 1 {
		t.Errorf("issue %s after %d card refunds, want pending after one", status, sent.Load())
	}
}

// TestRefundBeforeShipmentAllowsTheRestOfAnInvoiceAnAllowanceRelieves: ECPay
// refuses a void once an allowance exists, so none is asked for; the rest of
// the refund goes to the buyer as an allowance instead.
func TestRefundBeforeShipmentAllowsTheRestOfAnInvoiceAnAllowanceRelieves(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	fake, invoices := fakeECPay(t, time.Now())
	var sent atomic.Int64
	s := refunds.NewStore(pool, admintest.Refunder{Sent: &sent}, invoices)
	number, orderID := capturedOrderOwingAnInvoice(t, ctx, invoices)
	issueNow(t, ctx, invoices, orderID)
	relievePart(t, orderID, 100000)

	if got := pressRefund(t, ctx, s, number); got != "/admin/orders/"+number+"?refunded=1" {
		t.Fatalf("press redirected to %s, want the order cancelled with the rest allowed", got)
	}
	var voids int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM invoice_operations WHERE order_id = $1 AND kind = 'void'`,
		orderID).Scan(&voids); err != nil {
		t.Fatalf("count voids: %v", err)
	}
	allowance := allowanceOf(t, ctx, orderID)
	if voids != 0 || fake.Calls("/B2CInvoice/Invalid") != 0 || sent.Load() != 1 ||
		allowance.amount != 400000 || allowance.sends != 1 {
		t.Errorf("%d voids claimed, %d Invalid calls, %d card refunds, allowance %+v; "+
			"want no void, the refund, and an allowance for the 400000 left",
			voids, fake.Calls("/B2CInvoice/Invalid"), sent.Load(), allowance)
	}
}

// relievePart files an allowance of cents against the order's invoice, as an
// earlier partial refund would have.
func relievePart(t *testing.T, orderID uuid.UUID, cents int64) {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// Triggers off only because the refund that allowance relieved is not part
	// of this fixture.
	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatalf("disable triggers: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		WITH allowance AS (
			INSERT INTO invoice_documents (order_id, kind, original_id, number, amount_cents)
			SELECT order_id, 'allowance', id, $2, $3 FROM invoice_documents
			WHERE order_id = $1 AND kind = 'invoice'
			RETURNING id)
		INSERT INTO invoice_document_lines
		    (document_id, description, quantity, unit_price_cents, amount_cents, tax_type, position)
		SELECT id, '退貨折讓', 1, $3, $3, 'taxable', 0 FROM allowance`,
		orderID, uuid.NewString()[:32], cents); err != nil {
		t.Fatalf("file allowance: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestTheCancellationCountsAnAllowanceSentToTheBuyer: an allowance ECPay has
// e-mailed relieves its amount, whether it waits for the buyer or has lapsed;
// one never sent, or for less than the invoice, does not.
func TestTheCancellationCountsAnAllowanceSentToTheBuyer(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := refunds.NewStore(pool, admintest.Refunder{}, nil)
	for _, tt := range []struct {
		name   string
		status string
		sends  int
		cents  int64
		cancel bool
	}{
		{name: "awaiting the buyer", status: "pending", sends: 1, cents: 500000, cancel: true},
		{name: "lapsed", status: "attention", sends: 1, cents: 500000, cancel: true},
		{name: "never sent", status: "pending", sends: 0, cents: 500000},
		{name: "part of the invoice", status: "pending", sends: 1, cents: 250000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, true)
			if _, err := pool.Exec(ctx, `
				INSERT INTO invoice_documents (order_id, kind, number, amount_cents)
				VALUES ($1, 'invoice', $2, 500000)`, orderID, uuid.NewString()[:32]); err != nil {
				t.Fatalf("issue invoice: %v", err)
			}
			if _, err := s.RefundBeforeShipment(ctx, number, "顧客取消"); err == nil {
				t.Fatal("the order cancelled with its invoice live")
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO invoice_operations (order_id, kind, target_document_id, provider_key,
				    amount_cents, request_payload, actor_user_id, actor_id_snapshot, request_id,
				    status, send_attempts, last_send_at, available_at)
				SELECT d.order_id, 'allowance', d.id, d.number, $2, '{}', $3, $3, $4, $5, $6,
				       CASE WHEN $6 > 0 THEN now() END, now() + interval '1 day'
				FROM invoice_documents d WHERE d.order_id = $1 AND d.kind = 'invoice'`,
				orderID, tt.cents, staff, uuid.NewString(), tt.status, tt.sends); err != nil {
				t.Fatalf("record the allowance: %v", err)
			}
			_, err := s.RefundBeforeShipment(ctx, number, "")
			if cancelled := admintest.FulfillmentOf(t, pool, orderID) == "cancelled"; cancelled != tt.cancel {
				t.Errorf("order cancelled = %v (%v), want %v", cancelled, err, tt.cancel)
			}
		})
	}
}

// TestASentAllowanceECPayDidNotAcceptStillCancelsAndIsListed: the cancellation
// counts an allowance once it is sent, whatever ECPay answered. What makes that
// safe is that the operation is on /admin/health with its reason and the order
// page says why, rather than that it was e-mailed to the customer.
func TestASentAllowanceECPayDidNotAcceptStillCancelsAndIsListed(t *testing.T) {
	for _, tt := range []struct {
		name   string
		answer invoice.FakeAllowanceAnswer
		status string
		reason string
	}{
		{name: "no reply", answer: invoice.FakeAllowanceNoReply,
			status: "pending", reason: "allowance_send_ambiguous"},
		{name: "reply naming another invoice", answer: invoice.FakeAllowanceOtherInvoice,
			status: "attention", reason: invoice.CategorySuccessMismatch},
		{name: "amount still held", answer: invoice.FakeAllowanceAmountHeld,
			status: "attention", reason: invoice.CategoryAmountStillHeld},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := admintest.StaffContext(t, pool)
			fake, invoices := fakeECPay(t, time.Now().AddDate(0, -4, 0))
			var sent atomic.Int64
			s := refunds.NewStore(pool, admintest.Refunder{Sent: &sent}, invoices)
			number, orderID := capturedOrderOwingAnInvoice(t, ctx, invoices)
			issueNow(t, ctx, invoices, orderID)
			fake.AnswerAllowances(tt.answer)

			if got := pressRefund(t, ctx, s, number); got != "/admin/orders/"+number+"?refunded=1" {
				t.Fatalf("press redirected to %s, want the order cancelled with its allowance sent", got)
			}
			if got := admintest.FulfillmentOf(t, pool, orderID); got != "cancelled" || sent.Load() != 1 {
				t.Errorf("order %s after %d card refunds, want cancelled after one", got, sent.Load())
			}
			allowance := allowanceOf(t, ctx, orderID)
			var reason string
			if err := pool.QueryRow(ctx, `
				SELECT coalesce(last_error, '') FROM invoice_operations WHERE id = $1`,
				allowance.id).Scan(&reason); err != nil {
				t.Fatalf("read the allowance: %v", err)
			}
			if allowance.status != tt.status || reason != tt.reason || allowance.sends != 1 {
				t.Errorf("allowance %s/%s after %d sends, want %s/%s after one",
					allowance.status, reason, allowance.sends, tt.status, tt.reason)
			}

			// Health lists a pending operation once it is old enough to be stuck.
			if _, err := pool.Exec(ctx, `
				UPDATE invoice_operations SET created_at = now() - interval '16 minutes'
				WHERE id = $1`, allowance.id); err != nil {
				t.Fatalf("age the allowance: %v", err)
			}
			workers, err := health.NewStore(pool).WorkerHealth(ctx,
				outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
			if err != nil {
				t.Fatalf("health: %v", err)
			}
			listed := ""
			for _, c := range workers.StrandedClaims {
				if c.Operation == allowance.id.String() {
					listed = c.LastError
				}
			}
			if listed != tt.reason {
				t.Errorf("health lists the allowance with reason %q, want %q", listed, tt.reason)
			}

			var view admin.OrderView
			if err := invoicing.NewStore(pool, invoices, invoices).FillOrder(ctx, &view, number); err != nil {
				t.Fatalf("fill the order page: %v", err)
			}
			if tt.status == "pending" && view.AllowanceAwaitingUntil == "" ||
				tt.status == "attention" && (view.AllowanceAttention != tt.reason || view.AllowanceAwaitingUntil != "") {
				t.Errorf("order page shows awaiting %q, attention %q; want %s with reason %s",
					view.AllowanceAwaitingUntil, view.AllowanceAttention, tt.status, tt.reason)
			}
		})
	}
}
