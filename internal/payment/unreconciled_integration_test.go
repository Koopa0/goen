//go:build integration

package payment_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
)

// TestAnUnsettledCompletedSessionIsRecordedForAPerson holds the difference
// between classifying a delayed-method completion and leaving a person
// something to act on. The payment stays open so a later
// async_payment_succeeded can still capture through the existing door.
func TestAnUnsettledCompletedSessionIsRecordedForAPerson(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	var logs bytes.Buffer
	h := payment.NewHandler(s, enabledGateway(t), access.NewStore(pool, false),
		slog.New(slog.NewTextHandler(&logs, nil)))

	number, _ := order(t, 67000)
	session := "cs_unsettled_" + uuid.NewString()[:12]
	if err := s.OpenPayment(ctx, number, session, 67000); err != nil {
		t.Fatalf("open the known payment: %v", err)
	}
	eventID := "evt_" + uuid.NewString()[:12]
	body, header := signed(t, sessionEvent(eventID, session, "unpaid", 67000))

	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/webhooks/stripe", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", header)
	w := httptest.NewRecorder()
	h.Webhook(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Webhook() status = %d, want 200 — Stripe must not retry a session that cannot settle here", w.Code)
	}

	var processedAt *time.Time
	var reason *string
	var objectRef string
	if err := pool.QueryRow(ctx, `
		SELECT processed_at, unreconciled, coalesce(object_ref, '')
		FROM payment_webhook_events
		WHERE provider = 'stripe' AND event_id = $1`, eventID).
		Scan(&processedAt, &reason, &objectRef); err != nil {
		t.Fatalf("read unsettled event: %v", err)
	}
	if processedAt == nil {
		t.Error("the unsettled event was not marked processed, so Stripe will retry identical bytes")
	}
	if reason == nil || !strings.HasPrefix(*reason, "unsettled_session: ") {
		t.Fatalf("unreconciled = %v, want durable unsettled_session cause", reason)
	}
	if objectRef != session {
		t.Errorf("object_ref = %q, want the known session %q", objectRef, session)
	}

	backOffice := health.NewStore(pool)
	page, err := backOffice.WorkerHealth(
		ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)),
	)
	if err != nil {
		t.Fatalf("read health: %v", err)
	}
	var listed bool
	for _, event := range page.UnreconciledEvents {
		if event.EventID == eventID && event.Ref == session {
			listed = true
			break
		}
	}
	if !listed {
		t.Fatalf("health does not list unsettled session %q", session)
	}

	var paymentStatus string
	var captured *int64
	if err := pool.QueryRow(ctx, `
		SELECT status, captured_amount_cents FROM payments WHERE provider_ref = $1`, session).
		Scan(&paymentStatus, &captured); err != nil {
		t.Fatalf("read open payment: %v", err)
	}
	if paymentStatus != "requires_payment" || captured != nil {
		t.Errorf("unsettled completion left payment=%q captured=%v, want requires_payment/NULL",
			paymentStatus, captured)
	}
	if output := logs.String(); !strings.Contains(output, "level=ERROR") ||
		!strings.Contains(output, "delayed payment method") {
		t.Errorf("unsettled event log = %q, want ERROR naming the delayed method", output)
	}

	clearedID := "evt_" + uuid.NewString()[:12]
	clearedBody, clearedHeader := signed(t, typed(sessionEvent(clearedID, session, "paid", 67000),
		"checkout.session.async_payment_succeeded"))
	clearedReq := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/webhooks/stripe", bytes.NewReader(clearedBody))
	clearedReq.Header.Set("Stripe-Signature", clearedHeader)
	cleared := httptest.NewRecorder()
	h.Webhook(cleared, clearedReq)
	if cleared.Code != http.StatusOK {
		t.Fatalf("async_payment_succeeded status = %d, want 200", cleared.Code)
	}
	var clearedStatus string
	var clearedCaptured *int64
	if err := pool.QueryRow(ctx, `
		SELECT status, captured_amount_cents FROM payments WHERE provider_ref = $1`, session).
		Scan(&clearedStatus, &clearedCaptured); err != nil {
		t.Fatalf("read captured payment: %v", err)
	}
	if clearedStatus != "succeeded" || clearedCaptured == nil || *clearedCaptured != 67000 {
		t.Errorf("after async_payment_succeeded payment=%q captured=%v, want succeeded/67000",
			clearedStatus, clearedCaptured)
	}
	var stillFlagged *string
	if err := pool.QueryRow(ctx,
		`SELECT unreconciled FROM payment_webhook_events WHERE event_id = $1`,
		eventID).Scan(&stillFlagged); err != nil {
		t.Fatalf("reread unsettled event: %v", err)
	}
	if stillFlagged == nil || *stillFlagged != *reason {
		t.Errorf("later capture rewrote the unsettled alarm from %v to %v", reason, stillFlagged)
	}
}

// TestAnUnreadableKnownEventIsRecordedForAPerson drives the signature verifier,
// handler switch and durable alarm together. The payment row is real and open,
// so this cannot pass by accidentally taking the unattributed-capture branch.
func TestAnUnreadableKnownEventIsRecordedForAPerson(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	var logs bytes.Buffer
	h := payment.NewHandler(s, enabledGateway(t), access.NewStore(pool, false),
		slog.New(slog.NewTextHandler(&logs, nil)))

	number, _ := order(t, 67000)
	session := "cs_unreadable_" + uuid.NewString()[:12]
	if err := s.OpenPayment(ctx, number, session, 67000); err != nil {
		t.Fatalf("open the known payment: %v", err)
	}
	eventID := "evt_" + uuid.NewString()[:12]
	event := sessionField(sessionEvent(eventID, session, "paid", 67000),
		"amount_total", "67000")
	// A version mismatch is accepted, but acceptance does not make a malformed
	// actionable object readable. It must still become durable operator work.
	event["api_version"] = legacyWebhookAPIVersion
	body, header := signed(t, event)

	post := func() *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/webhooks/stripe", bytes.NewReader(body))
		req.Header.Set("Stripe-Signature", header)
		w := httptest.NewRecorder()
		h.Webhook(w, req)
		return w
	}
	if w := post(); w.Code != http.StatusOK {
		t.Fatalf("Webhook() status = %d, want 200 — retrying the same unreadable bytes cannot repair them", w.Code)
	}

	var processedAt *time.Time
	var reason *string
	var objectRef, storedPayload string
	if err := pool.QueryRow(ctx, `
		SELECT processed_at, unreconciled, coalesce(object_ref, ''), payload::text
		FROM payment_webhook_events
		WHERE provider = 'stripe' AND event_id = $1`, eventID).
		Scan(&processedAt, &reason, &objectRef, &storedPayload); err != nil {
		t.Fatalf("read unreadable event: %v", err)
	}
	if processedAt == nil {
		t.Error("the unreadable event was not marked processed, so Stripe will retry identical bytes")
	}
	if reason == nil || !strings.HasPrefix(*reason, "unreadable_event: ") {
		t.Fatalf("unreconciled = %v, want durable unreadable_event cause", reason)
	}
	if objectRef != session {
		t.Errorf("object_ref = %q, want the known session %q", objectRef, session)
	}

	var actionableRows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM payment_webhook_events
		WHERE provider = 'stripe' AND event_id = $1
		  AND unreconciled IS NOT NULL AND reconciled_at IS NULL`, eventID).
		Scan(&actionableRows); err != nil {
		t.Fatalf("read /admin/health predicate: %v", err)
	}
	if actionableRows != 1 {
		t.Errorf("/admin/health predicate finds %d unreadable events, want 1", actionableRows)
	}

	var paymentStatus, orderStatus string
	var captured *int64
	if err := pool.QueryRow(ctx, `
		SELECT p.status, p.captured_amount_cents, o.fulfillment_status
		FROM payments p JOIN orders o ON o.id = p.order_id
		WHERE p.provider_ref = $1`, session).
		Scan(&paymentStatus, &captured, &orderStatus); err != nil {
		t.Fatalf("read untouched payment: %v", err)
	}
	if paymentStatus != "requires_payment" || captured != nil || orderStatus != "pending" {
		t.Errorf("unreadable event left payment=%q captured=%v order=%q, want requires_payment/NULL/pending",
			paymentStatus, captured, orderStatus)
	}
	if output := logs.String(); !strings.Contains(output, "level=ERROR") ||
		!strings.Contains(output, "could not be read") {
		t.Errorf("unreadable event log = %q, want ERROR telling the operator it could not be read", output)
	}

	// A replay is still 200 and changes none of the evidence: the claim, reason
	// and original payload are history after the first signed delivery.
	if w := post(); w.Code != http.StatusOK {
		t.Fatalf("replayed Webhook() status = %d, want 200", w.Code)
	}
	var replayProcessedAt *time.Time
	var replayReason *string
	var replayPayload string
	if err := pool.QueryRow(ctx, `
		SELECT processed_at, unreconciled, payload::text
		FROM payment_webhook_events
		WHERE provider = 'stripe' AND event_id = $1`, eventID).
		Scan(&replayProcessedAt, &replayReason, &replayPayload); err != nil {
		t.Fatalf("read replayed event: %v", err)
	}
	if processedAt == nil || replayProcessedAt == nil || !processedAt.Equal(*replayProcessedAt) {
		t.Errorf("replay changed processed_at from %v to %v", processedAt, replayProcessedAt)
	}
	if reason == nil || replayReason == nil || *reason != *replayReason {
		t.Errorf("replay changed unreconciled from %v to %v", reason, replayReason)
	}
	if replayPayload != storedPayload {
		t.Error("replay replaced the original signed payload")
	}
}

// TestTheWebhookFlagsMoneyItCannotAttribute proves that a paid Checkout Session
// with no local payment row is countable, stays unguessed, and is acknowledged
// with 200 because retrying cannot create the missing attribution. Resolving the
// operator alarm must not make that already-paid Stripe identity eligible to be
// attached to a newly active local payment later.
func TestTheWebhookFlagsMoneyItCannotAttribute(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	var logs bytes.Buffer
	h := payment.NewHandler(s, enabledGateway(t), access.NewStore(pool, false),
		slog.New(slog.NewTextHandler(&logs, nil)))

	number, _ := order(t, 88800)
	session := "cs_unattributable_" + uuid.NewString()[:12]
	eventID := "evt_" + uuid.NewString()[:12]
	body, header := signed(t, typed(sessionEvent(eventID, session, "paid", 88800),
		"checkout.session.completed"))
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/webhooks/stripe", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", header)
	w := httptest.NewRecorder()
	h.Webhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Webhook() status = %d, want 200 — an unattributable capture can never succeed on retry", w.Code)
	}
	var processedAt *time.Time
	var reason *string
	var objectRef string
	if err := pool.QueryRow(ctx, `
		SELECT processed_at, unreconciled, coalesce(object_ref, '')
		FROM payment_webhook_events
		WHERE provider = 'stripe' AND event_id = $1`, eventID).
		Scan(&processedAt, &reason, &objectRef); err != nil {
		t.Fatalf("read unattributed capture: %v", err)
	}
	if processedAt == nil {
		t.Error("the unattributed capture was not marked processed")
	}
	if reason == nil || !strings.HasPrefix(*reason, "unattributed_capture: ") {
		t.Fatalf("unreconciled = %v, want durable unattributed_capture cause", reason)
	}
	if objectRef != session {
		t.Errorf("object_ref = %q, want session %q for the Stripe search", objectRef, session)
	}

	var actionableRows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM payment_webhook_events
		WHERE provider = 'stripe' AND event_id = $1
		  AND unreconciled IS NOT NULL AND reconciled_at IS NULL`, eventID).
		Scan(&actionableRows); err != nil {
		t.Fatalf("read /admin/health predicate: %v", err)
	}
	if actionableRows != 1 {
		t.Errorf("/admin/health predicate finds %d unattributed captures, want 1", actionableRows)
	}
	var payments int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE provider_ref = $1`, session).Scan(&payments); err != nil {
		t.Fatalf("count invented payments: %v", err)
	}
	if payments != 0 {
		t.Errorf("the handler invented %d payment rows for an unattributable session, want 0", payments)
	}
	if output := logs.String(); !strings.Contains(output, "level=ERROR") ||
		!strings.Contains(output, "cannot attribute") {
		t.Errorf("unattributed capture log = %q, want an actionable ERROR", output)
	}

	// Exact late-link regression: the event has no payment row, then an operator
	// reconciles it. reconciled_at resolves the alarm and permits a NEW Stripe
	// identity; it never erases the fact that this particular Session was paid.
	var reconciled bool
	if err := pool.QueryRow(ctx, `SELECT release_payment_event($1)`, eventID).
		Scan(&reconciled); err != nil || !reconciled {
		t.Fatalf("reconcile unattributed capture = %v, %v; want true", reconciled, err)
	}
	openErr := s.OpenPayment(ctx, number, session, 88800)
	if !errors.Is(openErr, payment.ErrNotOpenable) {
		t.Fatalf("OpenPayment with reconciled captured reference = %v, want ErrNotOpenable", openErr)
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](openErr)
	if !ok || pgErr.ConstraintName != "payments_open_refuses_seen_provider_ref" {
		t.Fatalf("late-link refusal = %v, want payments_open_refuses_seen_provider_ref", openErr)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE provider_ref = $1`, session).Scan(&payments); err != nil {
		t.Fatalf("count payments after refused late link: %v", err)
	}
	if payments != 0 {
		t.Errorf("reconciled unattributed reference gained %d payment rows, want 0", payments)
	}
}

// TestTheWebhookPersistsCapturesLocalInvariantsRefuse proves that verified money
// is never left only in a retry loop. Neither an amount that differs from the
// session intent nor an order whose amount changed can become valid when Stripe
// redelivers the same event, so both must be durable unreconciled outcomes.
func TestTheWebhookPersistsCapturesLocalInvariantsRefuse(t *testing.T) {
	tests := []struct {
		name       string
		captured   int64
		wantDetail string
	}{
		{name: "capture differs from session intent", captured: 130000, wantDetail: "against an intent of 100000"},
		{name: "order now owes a different amount", captured: 100000, wantDetail: "payments_capture_matches_order"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			s := payment.NewStore(pool)
			var logs bytes.Buffer
			h := payment.NewHandler(s, enabledGateway(t), access.NewStore(pool, false),
				slog.New(slog.NewTextHandler(&logs, nil)))

			number, id := holdableOrder(t, 100000)
			hold(t, id, 0, 45*time.Minute, "refused:"+number)
			session := "cs_refused_" + uuid.NewString()[:12]
			if err := s.OpenPayment(ctx, number, session, 100000); err != nil {
				t.Fatalf("open: %v", err)
			}
			if _, err := pool.Exec(ctx,
				`UPDATE orders SET shipping_cents = 30000 WHERE id = $1`, id); err != nil {
				t.Fatalf("change what the order owes: %v", err)
			}

			eventID := "evt_refused_" + uuid.NewString()[:12]
			body, header := signed(t, typed(sessionEvent(eventID, session, "paid", tt.captured),
				"checkout.session.completed"))
			req := httptest.NewRequestWithContext(ctx, http.MethodPost,
				"/webhooks/stripe", bytes.NewReader(body))
			req.Header.Set("Stripe-Signature", header)
			w := httptest.NewRecorder()
			h.Webhook(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("Webhook() status = %d, want 200 — identical retries cannot repair this capture", w.Code)
			}
			var processed bool
			var reason *string
			if err := pool.QueryRow(ctx, `
				SELECT processed_at IS NOT NULL, unreconciled
				FROM payment_webhook_events WHERE event_id = $1`, eventID).
				Scan(&processed, &reason); err != nil {
				t.Fatalf("read webhook outcome: %v", err)
			}
			if !processed {
				t.Error("the refused capture was not marked processed")
			}
			if reason == nil || !strings.HasPrefix(*reason, "refused_capture: ") ||
				!strings.Contains(*reason, tt.wantDetail) {
				t.Errorf("unreconciled = %v, want refused_capture naming %q", reason, tt.wantDetail)
			}

			var status string
			var captured *int64
			if err := pool.QueryRow(ctx,
				`SELECT status, captured_amount_cents FROM payments WHERE provider_ref = $1`,
				session).Scan(&status, &captured); err != nil {
				t.Fatalf("read refused payment: %v", err)
			}
			if status != "requires_payment" || captured != nil {
				t.Errorf("refused payment = %q/%v, want requires_payment/NULL", status, captured)
			}
			if output := logs.String(); !strings.Contains(output, "level=ERROR") ||
				!strings.Contains(output, "reconcile or refund") {
				t.Errorf("refused capture log = %q, want an actionable ERROR", output)
			}

			attempt, err := s.Attempt(ctx, number, 130000)
			if err != nil {
				t.Fatalf("Attempt after refused capture: %v", err)
			}
			if !attempt.NeedsReconciliation {
				t.Fatal("verified money awaiting reconciliation did not block another checkout")
			}
			if openErr := s.OpenPayment(ctx, number, "cs_must_not_open_"+uuid.NewString()[:12], 130000); !errors.Is(openErr, payment.ErrNotOpenable) {
				t.Fatalf("OpenPayment during reconciliation = %v, want ErrNotOpenable", openErr)
			}

			newSession := "cs_after_reconcile_" + uuid.NewString()[:12]
			gateway, stripeCalls := gatewayRecordingCalls(t, newSession)
			start := payment.NewHandler(s, gateway, access.NewStore(pool, false),
				slog.New(slog.DiscardHandler))
			post := func() *httptest.ResponseRecorder {
				req := httptest.NewRequestWithContext(ctx, http.MethodPost,
					"/orders/"+number+"/pay", http.NoBody)
				req.SetPathValue("number", number)
				placedBy(t, req, number)
				res := httptest.NewRecorder()
				start.Start(res, req)
				return res
			}
			if res := post(); res.Code != http.StatusConflict {
				t.Fatalf("Start during reconciliation status = %d, want 409", res.Code)
			}
			if *stripeCalls != 0 {
				t.Fatalf("Start called Stripe %d times while old captured money was unresolved, want 0",
					*stripeCalls)
			}

			// This represents the operator's refund/investigation acknowledgement.
			// It atomically terminates the old attempt before lifting the gate.
			var reconciled bool
			if reconcileErr := pool.QueryRow(ctx, `SELECT release_payment_event($1)`, eventID).
				Scan(&reconciled); reconcileErr != nil || !reconciled {
				t.Fatalf("release_payment_event = %v, %v; want true", reconciled, reconcileErr)
			}
			if res := post(); res.Code != http.StatusSeeOther {
				t.Fatalf("Start after reconciliation status = %d, want 303", res.Code)
			}
			if *stripeCalls != 1 {
				t.Fatalf("Start after reconciliation called Stripe %d times, want 1", *stripeCalls)
			}

			var statuses []string
			rows, err := pool.Query(ctx,
				`SELECT status FROM payments WHERE order_id = $1 ORDER BY created_at, id`, id)
			if err != nil {
				t.Fatalf("read attempts after reconciliation: %v", err)
			}
			for rows.Next() {
				var status string
				if err := rows.Scan(&status); err != nil {
					rows.Close()
					t.Fatalf("scan attempt status: %v", err)
				}
				statuses = append(statuses, status)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				t.Fatalf("walk attempt statuses: %v", err)
			}
			rows.Close()
			if diff := cmp.Diff([]string{"cancelled", "requires_payment"}, statuses); diff != "" {
				t.Errorf("payment statuses after safe replacement (-want +got):\n%s", diff)
			}
		})
	}
}

// TestReachableCaptureConstraintsBecomeDurable binds the classifier to the
// actual PostgreSQL names. A rename or a constraint removed from the durable
// set would turn verified money back into a permanent 500/retry loop.
func TestReachableCaptureConstraintsBecomeDurable(t *testing.T) {
	tests := []struct {
		name       string
		constraint string
		prepare    func(*testing.T, context.Context, *payment.Store, string, uuid.UUID, string)
		cardLast4  string
		wantStatus string
	}{
		{
			name: "the order now owes a different amount", constraint: "payments_capture_matches_order",
			prepare: func(t *testing.T, ctx context.Context, _ *payment.Store, _ string, id uuid.UUID, _ string) {
				t.Helper()
				if _, err := pool.Exec(ctx,
					`UPDATE orders SET shipping_cents = shipping_cents + 1 WHERE id = $1`, id); err != nil {
					t.Fatalf("change amount owed after opening payment: %v", err)
				}
			},
		},
		{
			name: "an incomplete order", constraint: "payments_require_complete_order",
			prepare: func(t *testing.T, ctx context.Context, _ *payment.Store, _ string, id uuid.UUID, _ string) {
				t.Helper()
				if _, err := pool.Exec(ctx, `DELETE FROM order_private_data WHERE order_id = $1`, id); err != nil {
					t.Fatalf("remove required delivery data: %v", err)
				}
			},
		},
		{
			name: "malformed card evidence", constraint: "payments_last4_format",
			cardLast4: "not-four-digits",
			prepare:   func(*testing.T, context.Context, *payment.Store, string, uuid.UUID, string) {},
		},
		{
			name: "a paid event follows a terminal local session", constraint: "payments_no_regression",
			wantStatus: "cancelled",
			prepare: func(t *testing.T, ctx context.Context, _ *payment.Store, _ string, _ uuid.UUID, session string) {
				t.Helper()
				if _, err := pool.Exec(ctx, `SELECT cancel_payment($1)`, session); err != nil {
					t.Fatalf("cancel payment before its late capture: %v", err)
				}
			},
		},
		{
			name: "another capture already funds the order", constraint: "payments_one_capture_per_order",
			prepare: func(t *testing.T, ctx context.Context, _ *payment.Store, _ string, id uuid.UUID, _ string) {
				t.Helper()
				if _, err := pool.Exec(ctx, `
					INSERT INTO payments
					    (order_id, provider_ref, status, intended_amount_cents,
					     captured_amount_cents, paid_at)
					VALUES ($1, $2, 'succeeded', 100000, 100000, now())`,
					id, "cs_existing_capture_"+uuid.NewString()[:12]); err != nil {
					t.Fatalf("record the order's existing capture: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			s := payment.NewStore(pool)
			number, id := order(t, 100000)
			session := "cs_constraint_" + uuid.NewString()[:12]

			if err := s.OpenPayment(ctx, number, session, 100000); err != nil {
				t.Fatalf("open payment: %v", err)
			}
			tt.prepare(t, ctx, s, number, id, session)

			eventID := "evt_constraint_" + uuid.NewString()[:12]
			event := sessionEvent(eventID, session, "paid", 100000)
			if tt.cardLast4 != "" {
				object := event["data"].(map[string]any)["object"].(map[string]any)
				object["payment_intent"] = map[string]any{
					"id": "pi_" + uuid.NewString()[:12],
					"latest_charge": map[string]any{
						"id": "ch_" + uuid.NewString()[:12],
						"payment_method_details": map[string]any{
							"card": map[string]any{"brand": "visa", "last4": tt.cardLast4},
						},
					},
				}
			}
			body, header := signed(t, typed(event, "checkout.session.completed"))
			req := httptest.NewRequestWithContext(ctx, http.MethodPost,
				"/webhooks/stripe", bytes.NewReader(body))
			req.Header.Set("Stripe-Signature", header)
			res := httptest.NewRecorder()
			payment.NewHandler(s, enabledGateway(t), access.NewStore(pool, false),
				slog.New(slog.DiscardHandler)).Webhook(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("Webhook() status = %d, want 200", res.Code)
			}

			var processed bool
			var reason *string
			if err := pool.QueryRow(ctx,
				`SELECT processed_at IS NOT NULL, unreconciled
				 FROM payment_webhook_events WHERE event_id = $1`, eventID).
				Scan(&processed, &reason); err != nil {
				t.Fatalf("read durable refusal: %v", err)
			}
			if !processed {
				t.Error("the durable refusal was not marked processed")
			}
			if reason == nil || !strings.HasPrefix(*reason, "refused_capture: ") ||
				!strings.Contains(*reason, tt.constraint) {
				t.Errorf("unreconciled = %v, want refused_capture naming %s", reason, tt.constraint)
			}

			wantStatus := tt.wantStatus
			if wantStatus == "" {
				wantStatus = "requires_payment"
			}
			var status string
			var captured *int64
			if err := pool.QueryRow(ctx, `
				SELECT status, captured_amount_cents FROM payments WHERE provider_ref = $1`, session).
				Scan(&status, &captured); err != nil {
				t.Fatalf("read payment after durable refusal: %v", err)
			}
			if status != wantStatus || captured != nil {
				t.Errorf("refused payment = %q/%v, want %q/NULL", status, captured, wantStatus)
			}
		})
	}
}

// TestTheWebhookItselfFlagsMoneyForACancelledOrder drives the real handler. The
// test below drives a callback of its own, so it can only show that the store
// records the outcome, never that handler.go's switch asks it to.
func TestTheWebhookItselfFlagsMoneyForACancelledOrder(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	var logs bytes.Buffer
	h := payment.NewHandler(s, enabledGateway(t), access.NewStore(pool, false),
		slog.New(slog.NewTextHandler(&logs, nil)))

	number, id := order(t, 88800)
	session := "cs_handler_unrec_" + uuid.NewString()[:12]
	if err := s.OpenPayment(ctx, number, session, 88800); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
		 WHERE id = $1`, id); err != nil {
		t.Fatalf("cancel the order: %v", err)
	}

	eventID := "evt_" + uuid.NewString()[:12]
	body, header := signed(t, typed(sessionEvent(eventID, session, "paid", 88800),
		"checkout.session.completed"))
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/webhooks/stripe", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", header)
	w := httptest.NewRecorder()
	h.Webhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Webhook() status = %d, want 200 — a 5xx gets the endpoint disabled", w.Code)
	}
	var reason *string
	if err := pool.QueryRow(ctx,
		`SELECT unreconciled FROM payment_webhook_events WHERE event_id = $1`,
		eventID).Scan(&reason); err != nil {
		t.Fatalf("read the event: %v", err)
	}
	if reason == nil {
		t.Fatal("the webhook took money for a cancelled order and left nothing " +
			"for /admin/health to count — the shop finds out when the customer asks")
	}
	if !strings.HasPrefix(*reason, "cancelled_order_capture: ") {
		t.Errorf("unreconciled = %q, want durable cancelled_order_capture cause", *reason)
	}
	if output := logs.String(); !strings.Contains(output, "level=ERROR") ||
		!strings.Contains(output, "cancelled order") {
		t.Errorf("cancelled-order capture log = %q, want an actionable ERROR", output)
	}
}

// TestMoneyForACancelledOrderLeavesSomethingToActOn holds the difference
// between a log line and a record.
//
// A slow webhook races a cancel: the capture is refused by
// payments_refuse_cancelled_order, and the event is still marked processed —
// retrying changes nothing, and rolling the claim back would lose the only
// trace that money arrived. The money IS at Stripe against goods already back
// on the shelf, so the event is marked UNRECONCILED in the same transaction as
// the claim: an event may not be recorded as seen unless what it needs is
// recorded with it.
func TestMoneyForACancelledOrderLeavesSomethingToActOn(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, id := order(t, 77700)
	session := "cs_unreconciled_" + number

	if err := s.OpenPayment(ctx, number, session, 77700); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
		 WHERE id = $1`, id); err != nil {
		t.Fatalf("cancel the order: %v", err)
	}

	const eventID = "evt_unreconciled_probe"
	claimed, err := s.ProcessWebhook(ctx, &payment.WebhookEvent{
		ID: eventID, Type: "checkout.session.completed", ObjectRef: session,
		Payload: []byte(`{"probe":true}`),
	}, func(ctx context.Context, tx *payment.WebhookTx) error {
		_, captureErr := tx.Capture(ctx, payment.Capture{
			SessionID: session, AmountRecv: 77700, Currency: payment.Currency,
		})
		if !errors.Is(captureErr, payment.ErrOrderCancelled) {
			return captureErr
		}
		return tx.Unreconciled(ctx,
			"cancelled_order_capture: money arrived for an order that was already cancelled")
	})
	if err != nil {
		t.Fatalf("process the webhook: %v", err)
	}
	if !claimed {
		t.Fatal("the event was not claimed, so this proved nothing")
	}

	var processed bool
	var reason *string
	if err := pool.QueryRow(ctx, `
		SELECT processed_at IS NOT NULL, unreconciled
		FROM payment_webhook_events WHERE provider = 'stripe' AND event_id = $1`,
		eventID).Scan(&processed, &reason); err != nil {
		t.Fatalf("read the event: %v", err)
	}
	if !processed {
		t.Error("the event was not marked processed, so Stripe will retry something " +
			"that can never succeed")
	}
	if reason == nil {
		t.Fatal("money arrived for a cancelled order and the event records nothing " +
			"about it — a log line is not something /admin/health can count, and " +
			"nobody will refund it")
	}
	if !strings.Contains(*reason, "cancelled") {
		t.Errorf("the reason is %q, which does not say what happened", *reason)
	}
}
