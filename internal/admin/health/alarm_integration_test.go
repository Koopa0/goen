//go:build integration

package health_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/ui/pages/admin"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/admin/taxonomy"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/outbox"
)

func healthHandler(p *pgxpool.Pool) *health.Handler {
	log := slog.New(slog.DiscardHandler)
	return health.NewHandler(health.NewStore(p), outbox.NewStore(p, log), nil, nil, log)
}

func TestTheHealthPageNamesARefundThatDidNotLand(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := admintest.ReturnDesk(pool, admintest.Refunder{
		RefundErr: errors.New("read tcp 1.2.3.4:443: i/o timeout"),
	})

	requestID, orderNumber := admintest.ReturnedOrder(t, pool, 1)

	if err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}); err == nil {
		t.Fatal("a refund that timed out was reported as success")
	}

	view, err := health.NewStore(pool).WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if view.RefundsHealthy() {
		t.Error("a refund that never left reads as healthy")
	}

	// Found by identity, never by position: the suite is shuffled and other tests leave refunds behind.
	var found *admin.OpenRefund
	for i := range view.OpenRefunds {
		if view.OpenRefunds[i].Key == "return:"+requestID.String() {
			found = &view.OpenRefunds[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("the stalled refund for return %s is on no page — the row exists "+
			"only for reconciliation and nothing can read it", requestID)
	}
	want := admin.OpenRefund{
		OrderNumber: orderNumber,
		Key:         "return:" + requestID.String(),
		Status:      "pending",
		AmountCents: 100000,
		ProviderRef: "",
		Since:       found.Since,
	}
	if diff := cmp.Diff(want, *found); diff != "" {
		t.Errorf("WorkerHealth() open refund mismatch (-want +got):\n%s", diff)
	}
}

func TestRefundHealthCountExceedsItsBoundedDiagnosticSample(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	worker := outbox.NewStore(pool, slog.New(slog.DiscardHandler))
	before, err := health.NewStore(pool).WorkerHealth(ctx, worker)
	if err != nil {
		t.Fatalf("read refund count before sample crowd: %v", err)
	}
	_, orderNumber := admintest.ReturnedOrder(t, pool, 1)
	prefix := "health-count-" + uuid.NewString() + "-"
	if _, insertErr := pool.Exec(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents)
		SELECT p.id, $2 || g::text, 1
		FROM payments p
		JOIN orders o ON o.id = p.order_id
		CROSS JOIN generate_series(1, 25) g
		WHERE o.order_number = $1 AND p.status = 'succeeded'`, orderNumber, prefix); insertErr != nil {
		t.Fatalf("create open-refund sample crowd: %v", insertErr)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, cleanupErr := pool.Exec(cleanupCtx,
			`DELETE FROM refunds WHERE request_key LIKE $1 || '%'`, prefix); cleanupErr != nil {
			t.Errorf("remove open-refund sample crowd: %v", cleanupErr)
		}
	})

	after, err := health.NewStore(pool).WorkerHealth(ctx, worker)
	if err != nil {
		t.Fatalf("read refund count after sample crowd: %v", err)
	}
	if after.OpenRefundCount != before.OpenRefundCount+25 {
		t.Errorf("exact open refund count moved %d -> %d, want +25",
			before.OpenRefundCount, after.OpenRefundCount)
	}
	if len(after.OpenRefunds) != health.OpenRefundListLimit {
		t.Errorf("diagnostic sample has %d rows, want bounded %d",
			len(after.OpenRefunds), health.OpenRefundListLimit)
	}
	enCtx := i18n.WithLocale(ctx, i18n.En)
	wantText := fmt.Sprintf(i18n.T(enCtx, i18n.KeyHealthRefundsStuck), after.OpenRefundCount)
	if got := after.RefundsText(enCtx); got != wantText {
		t.Errorf("refund health text = %q, want exact count %q", got, wantText)
	}
}

// TestAnAcknowledgedPaymentLeavesTheAlarm is the other half of flagging one.
// The refund is at Stripe and nothing here can see it land, so the alarm has an
// off switch or /admin/health is unhealthy forever after the first arrival —
// and an alarm that is always on is one nobody reads.
func TestAnAcknowledgedPaymentLeavesTheAlarm(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	worker := outbox.NewStore(pool, slog.New(slog.DiscardHandler))
	baseline, healthErr := health.NewStore(pool).WorkerHealth(ctx, worker)
	if healthErr != nil {
		t.Fatalf("health before fixture: %v", healthErr)
	}

	number := admintest.PlaceUnpaidOrder(t, pool)
	var orderID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM orders WHERE order_number = $1`, number).
		Scan(&orderID); err != nil {
		t.Fatalf("read order: %v", err)
	}
	sessionID := "cs_ack_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx, `
		INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents)
		VALUES ($1, $2, 'requires_payment', 500000)`, orderID, sessionID); err != nil {
		t.Fatalf("open payment: %v", err)
	}

	eventID := "evt_ack_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_webhook_events
			(provider, event_id, type, object_ref, payload, unreconciled)
		VALUES ('stripe', $1, 'checkout.session.completed', $2, '{}'::jsonb,
		        'refused_capture: operator must refund or post the verified money')`,
		eventID, sessionID); err != nil {
		t.Fatalf("flag the event: %v", err)
	}

	flagged, err := health.NewStore(pool).WorkerHealth(ctx, worker)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if flagged.UnreconciledPayments != baseline.UnreconciledPayments+1 {
		t.Fatalf("flagging this event changed the alarm count from %d to %d, want one more",
			baseline.UnreconciledPayments, flagged.UnreconciledPayments)
	}
	var eventFlagged bool
	for _, event := range flagged.UnreconciledEvents {
		if event.EventID == eventID {
			eventFlagged = true
			break
		}
	}
	if !eventFlagged {
		t.Fatalf("payment alarm does not name flagged event %q", eventID)
	}

	// Merely naming the event would cancel the linked attempt and allow another
	// checkout without saying what happened to the provider money, so the HTTP
	// boundary must refuse a form that omits the safe-release conclusion.
	form := url.Values{"event": {eventID}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/health/reconcile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	healthHandler(pool).Reconcile(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/health?notflagged=1" {
		t.Fatalf("event-only HTTP resolution = %d %q, want refusal redirect",
			res.Code, res.Header().Get("Location"))
	}
	var stillOpen, stillFlagged bool
	if err := pool.QueryRow(ctx, `
		SELECT p.status = 'requires_payment', e.reconciled_at IS NULL
		FROM payments p JOIN payment_webhook_events e
		  ON e.provider = p.provider AND e.object_ref = p.provider_ref
		WHERE e.event_id = $1`, eventID).Scan(&stillOpen, &stillFlagged); err != nil {
		t.Fatalf("read refused event-only resolution: %v", err)
	}
	if !stillOpen || !stillFlagged {
		t.Fatalf("event-only resolution changed payment/event = open %v, flagged %v",
			stillOpen, stillFlagged)
	}

	beforeAudit := admintest.AuditRows(t, pool, audit.ActionReconcilePayment)
	if err := health.NewStore(pool).ReleasePaymentEventAfterRefundOrAccounting(ctx, eventID); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	var paymentStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM payments WHERE provider_ref = $1`, sessionID).
		Scan(&paymentStatus); err != nil {
		t.Fatalf("read reconciled payment: %v", err)
	}
	if paymentStatus != "cancelled" {
		t.Errorf("linked payment status = %q, want cancelled so a completed session cannot be resumed",
			paymentStatus)
	}

	settled, settledErr := health.NewStore(pool).WorkerHealth(ctx, worker)
	if settledErr != nil {
		t.Fatalf("health: %v", settledErr)
	}
	if settled.UnreconciledPayments != baseline.UnreconciledPayments {
		t.Errorf("reconciling this event left the alarm count at %d, want baseline %d",
			settled.UnreconciledPayments, baseline.UnreconciledPayments)
	}
	for _, event := range settled.UnreconciledEvents {
		if event.EventID == eventID {
			t.Errorf("acknowledged event %q remains on the payment alarm", eventID)
		}
	}
	if afterAudit := admintest.AuditRows(t, pool, audit.ActionReconcilePayment); afterAudit != beforeAudit+1 {
		t.Errorf("saying the money went back by hand added %d audit rows, want 1",
			afterAudit-beforeAudit)
	}

	// A second press changes nothing: the row count is where the question is
	// asked, so there is no read-then-write for two staff members to both pass.
	if err := health.NewStore(pool).ReleasePaymentEventAfterRefundOrAccounting(
		ctx, eventID,
	); !errors.Is(err, health.ErrNotFound) {
		t.Errorf("acknowledging it twice = %v, want ErrNotFound", err)
	}
	// And an event nobody flagged is not acknowledgeable at all.
	unflagged := "evt_plain_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_webhook_events (provider, event_id, type, payload)
		VALUES ('stripe', $1, 'payment_intent.processing', '{}'::jsonb)`, unflagged); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := health.NewStore(pool).ReleasePaymentEventAfterRefundOrAccounting(
		ctx, unflagged,
	); !errors.Is(err, health.ErrNotFound) {
		t.Errorf("acknowledging an event that was never flagged = %v, want ErrNotFound", err)
	}
}

// TestACompletePaymentWithoutAFlaggedEventHasAResolutionDoor covers provider
// completion before a capture webhook and the understood complete/unpaid event:
// neither has an unreconciled event row, so the payment state itself must make
// health unhealthy and give staff an audited, typed way to resolve it.
func TestACompletePaymentWithoutAFlaggedEventHasAResolutionDoor(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	worker := outbox.NewStore(pool, slog.New(slog.DiscardHandler))

	number := admintest.PlaceUnpaidOrder(t, pool)
	var orderID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM orders WHERE order_number = $1`, number).
		Scan(&orderID); err != nil {
		t.Fatalf("read order: %v", err)
	}
	providerRef := "cs_complete_health_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx, `
		INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents)
		VALUES ($1, $2, 'requires_reconciliation', 500000)`, orderID, providerRef); err != nil {
		t.Fatalf("record complete payment: %v", err)
	}
	// This is the complete+unpaid shape: the webhook was understood and clean,
	// so it cannot be resolved through release_payment_event.
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_webhook_events
			(provider, event_id, type, object_ref, payload, processed_at)
		VALUES ('stripe', $1, 'checkout.session.completed', $2, '{}'::jsonb, now())`,
		"evt_complete_unpaid_"+uuid.NewString()[:12], providerRef); err != nil {
		t.Fatalf("record understood complete event: %v", err)
	}

	flagged, err := health.NewStore(pool).WorkerHealth(ctx, worker)
	if err != nil {
		t.Fatalf("health before resolution: %v", err)
	}
	if flagged.PaymentsReconciled() {
		t.Fatal("requires_reconciliation payment with no flagged event reads healthy")
	}
	found := false
	for _, issue := range flagged.UnreconciledCompletePayments {
		if issue.ProviderRef == providerRef {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("health did not name complete provider reference %q", providerRef)
	}

	beforeAudit := admintest.AuditRows(t, pool, audit.ActionReconcilePayment)
	if reconcileErr := health.NewStore(pool).ReconcileCompletePayment(ctx, providerRef,
		health.CompletePaymentUnpaidOrRefunded); reconcileErr != nil {
		t.Fatalf("reconcile complete payment: %v", reconcileErr)
	}
	var status string
	if queryErr := pool.QueryRow(ctx,
		`SELECT status FROM payments WHERE provider_ref = $1`, providerRef).Scan(&status); queryErr != nil {
		t.Fatalf("read resolved payment: %v", queryErr)
	}
	if status != "reconciled" {
		t.Errorf("resolved complete payment status = %q, want reconciled", status)
	}

	settled, err := health.NewStore(pool).WorkerHealth(ctx, worker)
	if err != nil {
		t.Fatalf("health after resolution: %v", err)
	}
	for _, issue := range settled.UnreconciledCompletePayments {
		if issue.ProviderRef == providerRef {
			t.Errorf("resolved provider reference %q remains on health", providerRef)
		}
	}
	if got := admintest.AuditRows(t, pool, audit.ActionReconcilePayment); got != beforeAudit+1 {
		t.Errorf("reconciling complete payment added %d audit rows, want 1", got-beforeAudit)
	}
	if err := health.NewStore(pool).ReconcileCompletePayment(ctx, providerRef,
		health.CompletePaymentUnpaidOrRefunded); !errors.Is(err, health.ErrNotFound) {
		t.Errorf("reconciling complete payment twice = %v, want ErrNotFound", err)
	}
}

// TestReleasedStockPaidAttributionReturnsARefundInstruction proves the admin
// boundary does not turn the capture fence into a generic 500. Health removes
// the impossible paid action, and a stale form submitted across a concurrent
// sweep returns an actionable refund notice while leaving reconciliation open.
func TestReleasedStockPaidAttributionReturnsARefundInstruction(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	number, orderID, _ := admintest.PendingOrderHoldingStock(t, pool)
	providerRef := "cs_admin_released_stock_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx,
		`SELECT open_payment($1, $2, 100000)`, orderID, providerRef); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	var reservationID uuid.UUID
	if err := pool.QueryRow(ctx, `
		UPDATE inventory_reservations
		SET created_at = now() - interval '2 hours',
		    expires_at = now() - interval '1 hour'
		WHERE order_id = $1 AND state = 'held'
		RETURNING id`, orderID).Scan(&reservationID); err != nil {
		t.Fatalf("expire hold: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT release_reservation($1)`, reservationID); err != nil {
		t.Fatalf("release hold before recovery: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`SELECT record_complete_payment($1, $2, 100000)`, orderID, providerRef); err != nil {
		t.Fatalf("record delayed complete session: %v", err)
	}

	view, err := health.NewStore(pool).WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	refundOnly := false
	for _, issue := range view.UnreconciledCompletePayments {
		if issue.ProviderRef == providerRef && issue.OrderNumber == number &&
			!issue.PaidAttributionAllowed {
			refundOnly = true
		}
	}
	if !refundOnly {
		t.Fatal("released-stock complete payment still offered paid attribution")
	}

	form := url.Values{
		"payment":    {providerRef},
		"resolution": {"paid"},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/health/reconcile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	healthHandler(pool).Reconcile(res, req)
	if res.Code != http.StatusSeeOther ||
		res.Header().Get("Location") != "/admin/health?mustrefund=1" {
		t.Fatalf("stale paid form = %d %q, want actionable refund redirect",
			res.Code, res.Header().Get("Location"))
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM payments WHERE provider_ref = $1`, providerRef).Scan(&status); err != nil {
		t.Fatalf("read refused payment: %v", err)
	}
	if status != "requires_reconciliation" {
		t.Fatalf("refused paid form changed payment to %q, want requires_reconciliation", status)
	}
}

// TestARefusedSystemIssueIsOnTheHealthPage: nobody watches an automatic issue,
// so ECPay refusing it stays in front of a person until a later issue exists. A
// staff claim's refusal was shown to the person who pressed the button and is
// not listed.
func TestARefusedSystemIssueIsOnTheHealthPage(t *testing.T) {
	ctx, actor := admintest.StaffContext(t, pool)
	worker := outbox.NewStore(pool, slog.New(slog.DiscardHandler))

	refuse := func(number string, orderID uuid.UUID, kind string) {
		t.Helper()
		staffActor := uuid.NullUUID{UUID: actor, Valid: kind == "staff"}
		if _, err := pool.Exec(ctx, `
			INSERT INTO invoice_operations
			    (order_id, kind, provider_key, amount_cents, request_payload,
			     actor_user_id, actor_id_snapshot, actor_kind, request_id,
			     status, last_error, created_at)
			VALUES ($1, 'issue', replace($2, '-', ''), 100000, '{}', $3, $3, $4,
			        'refused:' || $2, 'rejected', 'issue_provider_rejected_2000006',
			        now() - interval '1 minute')`,
			orderID, number, staffActor, kind); err != nil {
			t.Fatalf("record a refused %s issue for %s: %v", kind, number, err)
		}
	}
	listed := func(number string) bool {
		t.Helper()
		view, err := health.NewStore(pool).WorkerHealth(ctx, worker)
		if err != nil {
			t.Fatalf("health: %v", err)
		}
		for _, c := range view.StrandedClaims {
			if c.OrderNumber == number {
				return true
			}
		}
		return false
	}

	systemNumber, systemOrder := admintest.PaidPickingOrderForUser(t, pool, admintest.Customer(t, pool), 100000)
	refuse(systemNumber, systemOrder, "system")
	staffNumber, staffOrder := admintest.PaidPickingOrderForUser(t, pool, admintest.Customer(t, pool), 100000)
	refuse(staffNumber, staffOrder, "staff")
	creditNumber, creditOrder, _ := admintest.PaidUnshippedOrder(t, pool, 0, 100000, false)
	refuse(creditNumber, creditOrder, "system")

	if !listed(systemNumber) {
		t.Error("a refused automatic issue is not on /admin/health; the paid order " +
			"goes uninvoiced with nothing to say so")
	}
	if !listed(creditNumber) {
		t.Error("a refused automatic issue on an order store credit paid in full is not " +
			"on /admin/health; the order is pending, and owes its invoice all the same")
	}
	if listed(staffNumber) {
		t.Error("a staff claim's refusal is on /admin/health; the person who pressed " +
			"the button already saw it")
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_operations
		    (order_id, kind, provider_key, amount_cents, request_payload,
		     actor_user_id, actor_id_snapshot, request_id)
		VALUES ($1, 'issue', replace($2, '-', '') || 'R1', 100000, '{}', $3, $3,
		        'retry:' || $2)`, systemOrder, systemNumber, actor); err != nil {
		t.Fatalf("issue %s again: %v", systemNumber, err)
	}
	if listed(systemNumber) {
		t.Error("the refused automatic issue is still listed after staff issued the order again")
	}
}

// TestAStrandedInvoiceClaimIsOnTheHealthPage holds the alarm and the only
// auditable recovery door for an aged ambiguous Allowance.
//
// A 折讓 claim is taken before ECPay is asked, because their allowance endpoint
// carries no idempotency field. A call that was not ANSWERED keeps its claim —
// right, because whether the document was filed is not knowable from here — and
// that leaves a row nothing else can resolve, the shape
// payment_webhook_events.unreconciled already has.
func TestAStrandedInvoiceClaimIsOnTheHealthPage(t *testing.T) {
	ctx, actor := admintest.StaffContext(t, pool)
	worker := outbox.NewStore(pool, slog.New(slog.DiscardHandler))

	number, orderID, _, _ := admintest.TwoLineOrderWithStock(t, pool, "stranded")
	var invoiceID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoice_documents (order_id, kind, number, amount_cents)
		VALUES ($1, 'invoice', 'GD-' || substr(replace(gen_random_uuid()::text,'-',''),1,8), 100000)
		RETURNING id`, orderID).Scan(&invoiceID); err != nil {
		t.Fatalf("file an invoice: %v", err)
	}

	// A claim taken JUST NOW is a call in flight, not a stuck one: the window is
	// the whole distinction, so the durable operation fixture has to sit on the
	// far side of it. A pending invoice_documents row is not a tax document and
	// is deliberately no longer a state the schema admits.
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_operations
		    (order_id,kind,target_document_id,provider_key,amount_cents,
		     request_payload,actor_user_id,actor_id_snapshot,request_id,
		     send_attempts,last_send_at,last_error,created_at,updated_at)
		SELECT $1,'allowance',d.id,d.number,50000,
		       jsonb_build_object(
		           'invoice_number',d.number,
		           'invoice_date',to_char(shop_day(d.issued_at), 'YYYY-MM-DD'),
		           'customer_name','王小明','email','stranded@goen.invalid',
		           'amount_cents',50000,
		           'lines',jsonb_build_array(jsonb_build_object(
		               'description','退貨折讓','quantity',1,
		               'unit_price_cents',50000,'amount_cents',50000))),
		       $3,$3,$4,1,now()-interval '1 hour','allowance_not_yet_visible',
		       now()-interval '1 hour',now()-interval '1 hour'
		FROM invoice_documents d WHERE d.id=$2`,
		orderID, invoiceID, actor, "invoice-stranded:"+number); err != nil {
		t.Fatalf("strand a claim: %v", err)
	}

	view, err := health.NewStore(pool).WorkerHealth(ctx, worker)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if view.ClaimsSettled() {
		t.Fatal("a 折讓 claim the provider never answered is invisible on the one " +
			"page built to show what needs doing — the only sign is a button that " +
			"refuses, on one order, months later")
	}
	if view.AllHealthy() {
		t.Error("the page reads healthy with a claim nobody can settle outstanding")
	}
	var named, actionable bool
	var operation string
	for _, c := range view.StrandedClaims {
		if c.OrderNumber == number {
			named = true
			actionable = c.CanAuthorizeResend
			operation = c.Operation
		}
	}
	if !named {
		t.Errorf("the claim is counted and not named; an operator needs the order "+
			"to go and look at ECPay. Got %d claim(s).", len(view.StrandedClaims))
	}
	if !actionable {
		t.Fatal("an aged empty Allowance lookup has no explicit one-resend authorization door")
	}

	// Naming the operation is not confirmation. The typed conclusion is required
	// and an active worker lease makes even that conclusion ineligible.
	post := func(values url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/admin/health/reconcile", strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res := httptest.NewRecorder()
		healthHandler(pool).Reconcile(res, req)
		return res
	}
	omitted := post(url.Values{"invoice_operation": {operation}})
	if omitted.Code != http.StatusSeeOther ||
		omitted.Header().Get("Location") != "/admin/health?notflagged=1" {
		t.Fatalf("unconfirmed allowance form = %d %q, want refusal redirect",
			omitted.Code, omitted.Header().Get("Location"))
	}

	leaseOwner := uuid.New()
	if _, err := pool.Exec(ctx, `
		UPDATE invoice_operations
		SET lease_owner=$2, lease_until=now()+interval '1 minute'
		WHERE id=$1`, operation, leaseOwner); err != nil {
		t.Fatalf("hold a live worker lease: %v", err)
	}
	form := url.Values{
		"invoice_operation":  {operation},
		"invoice_resolution": {"confirmed_absent"},
	}
	leasing := post(form)
	if leasing.Code != http.StatusSeeOther ||
		leasing.Header().Get("Location") != "/admin/health?notflagged=1" {
		t.Fatalf("authorization during lease = %d %q, want refusal redirect",
			leasing.Code, leasing.Header().Get("Location"))
	}
	if _, err := pool.Exec(ctx, `
		UPDATE invoice_operations SET lease_owner=NULL, lease_until=NULL WHERE id=$1`,
		operation); err != nil {
		t.Fatalf("release worker lease: %v", err)
	}

	accepted := post(form)
	if accepted.Code != http.StatusSeeOther ||
		accepted.Header().Get("Location") != "/admin/health?invoicequeued=1" {
		t.Fatalf("confirmed allowance form = %d %q, want queued redirect",
			accepted.Code, accepted.Header().Get("Location"))
	}
	duplicate := post(form)
	if duplicate.Code != http.StatusSeeOther ||
		duplicate.Header().Get("Location") != "/admin/health?notflagged=1" {
		t.Fatalf("duplicate authorization = %d %q, want refusal redirect",
			duplicate.Code, duplicate.Header().Get("Location"))
	}

	var authorizations, audits int
	var auditActor uuid.UUID
	var auditRequest string
	if err := pool.QueryRow(ctx, `
		SELECT op.resend_authorizations,
		       (SELECT count(*)::integer FROM audit_events a
		        WHERE a.entity_id=op.id
		          AND a.action=$2),
		       coalesce((SELECT a.actor_id_snapshot FROM audit_events a
		                 WHERE a.entity_id=op.id
		                   AND a.action=$2
		                 ORDER BY a.occurred_at LIMIT 1),
		                '00000000-0000-0000-0000-000000000000'::uuid),
		       coalesce((SELECT a.request_id FROM audit_events a
		                 WHERE a.entity_id=op.id
		                   AND a.action=$2
		                 ORDER BY a.occurred_at LIMIT 1), '')
		FROM invoice_operations op
		WHERE op.id=$1`, operation, audit.ActionAuthorizeAllowanceResend).
		Scan(&authorizations, &audits, &auditActor, &auditRequest); err != nil {
		t.Fatalf("read authorization and audit: %v", err)
	}
	if authorizations != 1 || audits != 1 || auditActor != actor ||
		auditRequest != "req-"+actor.String()[:8] {
		t.Fatalf("authorization/audit = %d/%d actor %s request %q",
			authorizations, audits, auditActor, auditRequest)
	}
}

// The health page counts uploads nothing references; a category's photograph
// is a reference.
func TestAUploadHeldByACategoryIsNotCountedUnreferenced(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	messages := outbox.NewStore(pool, slog.New(slog.DiscardHandler))
	count := func() int64 {
		t.Helper()
		page, err := health.NewStore(pool).WorkerHealth(ctx, messages)
		if err != nil {
			t.Fatalf("read health: %v", err)
		}
		return page.UnreferencedMedia
	}

	before := count()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	digest := hex.EncodeToString(raw)
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_objects (digest, content_type, byte_size, width, height, bytes, created_at)
		VALUES ($1, 'image/png', 1, 1, 1, '\x00', now() - interval '48 hours')`, digest); err != nil {
		t.Fatalf("store an old upload: %v", err)
	}
	if got := count(); got != before+1 {
		t.Fatalf("an unreferenced upload counts %d, want %d", got, before+1)
	}

	slug := "held-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	if errs, err := taxonomy.NewStore(pool).CreateCategory(ctx, &taxonomy.Form{Slug: slug, Name: "持有照片"}); err != nil || len(errs) > 0 {
		t.Fatalf("create: %v %v", errs, err)
	}
	if err := taxonomy.NewStore(pool).SetCategoryImage(ctx, slug, digest, "照片", ""); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if got := count(); got != before {
		t.Errorf("a category's photograph counts %d unreferenced, want %d", got, before)
	}
}

// TestALapsedOnlineAllowanceOffersOneResend: an allowance the buyer never
// agreed to within 72 hours is on the page with the audited resend, worded as
// asking the buyer again rather than as a request ECPay never received.
func TestALapsedOnlineAllowanceOffersOneResend(t *testing.T) {
	ctx, actor := admintest.StaffContext(t, pool)
	worker := outbox.NewStore(pool, slog.New(slog.DiscardHandler))
	number, orderID, _, _ := admintest.TwoLineOrderWithStock(t, pool, "lapsed")
	if _, err := pool.Exec(ctx, `
		WITH invoice AS (
			INSERT INTO invoice_documents (order_id, kind, number, amount_cents)
			VALUES ($1, 'invoice', 'GD-' || substr(replace(gen_random_uuid()::text,'-',''),1,8), 100000)
			RETURNING id, order_id, number)
		INSERT INTO invoice_operations
		    (order_id, kind, target_document_id, provider_key, amount_cents, request_payload,
		     actor_user_id, actor_id_snapshot, request_id, status, send_attempts, last_send_at, last_error)
		SELECT order_id, 'allowance', id, number, 50000, '{}', $2, $2, $3,
		       'attention', 1, now() - interval '73 hours', 'allowance_buyer_unconfirmed'
		FROM invoice`, orderID, actor, "invoice-lapsed:"+number); err != nil {
		t.Fatalf("record a lapsed allowance: %v", err)
	}

	view, err := health.NewStore(pool).WorkerHealth(ctx, worker)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	for _, c := range view.StrandedClaims {
		if c.OrderNumber == number {
			if !c.CanAuthorizeResend || !c.BuyerNeverAgreed() {
				t.Errorf("lapsed allowance offers resend %v, worded as never agreed %v; want both",
					c.CanAuthorizeResend, c.BuyerNeverAgreed())
			}
			return
		}
	}
	t.Errorf("the lapsed allowance of %s is not on the health page", number)
}

// With no 加值中心 an issue a paid sale owes is claimed and waits for one to be
// configured (invoice.Store.ClaimDue). It is not stranded, so the page and the
// dashboard task must not raise it; an operation already sent still is.
func TestAnIssueWaitingForNoProviderIsNotAStrandedClaim(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `
		UPDATE invoice_operations SET created_at = now() - interval '1 hour' WHERE order_id = $1`, orderID); err != nil {
		t.Fatalf("age the claim: %v", err)
	}
	worker := outbox.NewStore(pool, slog.New(slog.DiscardHandler))
	stranded := func(s *health.Store) bool {
		t.Helper()
		view, err := s.WorkerHealth(ctx, worker)
		if err != nil {
			t.Fatalf("health: %v", err)
		}
		for _, c := range view.StrandedClaims {
			if c.OrderNumber == number {
				return true
			}
		}
		return false
	}
	if !stranded(health.NewStore(pool).WithInvoicing(true)) {
		t.Fatal("with e-invoicing on, an hour-old unsent issue is not listed as stranded")
	}
	if stranded(health.NewStore(pool).WithInvoicing(false)) {
		t.Error("with e-invoicing off, an unsent issue is listed as a stranded claim")
	}
	if _, err := pool.Exec(ctx, `UPDATE invoice_operations SET send_attempts = 1 WHERE order_id = $1`, orderID); err != nil {
		t.Fatalf("mark the issue sent: %v", err)
	}
	if !stranded(health.NewStore(pool).WithInvoicing(false)) {
		t.Error("with e-invoicing off, an issue already sent to ECPay is not listed")
	}
}
