//go:build integration

package payment_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	stripe "github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/payment"
)

func disputePayload(
	eventID, disputeID, chargeID, piID, status string,
	amount int64, created int64, dueBy *int64,
	movements []map[string]any,
) map[string]any {
	obj := map[string]any{
		"id": disputeID, "object": "dispute", "status": status,
		"amount": amount, "currency": "twd",
		"charge":         map[string]any{"id": chargeID, "object": "charge"},
		"payment_intent": map[string]any{"id": piID, "object": "payment_intent"},
	}
	if dueBy != nil {
		obj["evidence_details"] = map[string]any{"due_by": *dueBy}
	}
	if len(movements) > 0 {
		obj["balance_transactions"] = movements
	}
	return map[string]any{
		"id": eventID, "object": "event", "api_version": stripe.APIVersion,
		"type": "charge.dispute.created", "created": created,
		"data": map[string]any{"object": obj},
	}
}

func paidSessionWithPI(eventID, sessionID, piID, chargeID string, amount int64) map[string]any {
	return map[string]any{
		"id": eventID, "object": "event", "api_version": stripe.APIVersion,
		"type": "checkout.session.completed",
		"data": map[string]any{
			"object": map[string]any{
				"id": sessionID, "object": "checkout.session", "payment_status": "paid",
				"amount_total": amount, "currency": "twd",
				"payment_intent": map[string]any{
					"id": piID, "object": "payment_intent",
					"latest_charge": map[string]any{
						"id": chargeID, "object": "charge",
						"payment_method_details": map[string]any{
							"card": map[string]any{"brand": "visa", "last4": "4242"},
						},
					},
				},
			},
		},
	}
}

func capturePaidOrder(
	t *testing.T, ctx context.Context, s *payment.Store, h *payment.Handler,
	number, session, piID, chargeID string, amount int64,
) {
	t.Helper()
	if err := s.OpenPayment(ctx, number, session, amount); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	eventID := "evt_cap_" + uuid.NewString()[:10]
	body, header := signed(t, paidSessionWithPI(eventID, session, piID, chargeID, amount))
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/webhooks/stripe", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", header)
	w := httptest.NewRecorder()
	h.Webhook(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("capture webhook status = %d", w.Code)
	}
}

func postSignedWebhook(t *testing.T, ctx context.Context, h *payment.Handler, ev map[string]any) {
	t.Helper()
	body, header := signed(t, ev)
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/webhooks/stripe", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", header)
	w := httptest.NewRecorder()
	h.Webhook(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("webhook status = %d", w.Code)
	}
}

func disputeHandler(t *testing.T, s *payment.Store) *payment.Handler {
	t.Helper()
	return payment.NewHandler(s, enabledGateway(t), alwaysPlacedHere{}, slog.New(slog.DiscardHandler), false)
}

func TestDisputeWebhookAttributesOrder(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	h := disputeHandler(t, s)
	number, _ := order(t, 150000)
	session := "cs_dispute_" + uuid.NewString()[:8]
	piID := "pi_dispute_" + uuid.NewString()[:8]
	chargeID := "ch_dispute_" + uuid.NewString()[:8]
	capturePaidOrder(t, ctx, s, h, number, session, piID, chargeID, 150000)

	due := time.Now().Add(48 * time.Hour).Unix()
	eventID := "evt_dispute_" + uuid.NewString()[:8]
	disputeID := "dp_dispute_" + uuid.NewString()[:8]
	ev := disputePayload(eventID, disputeID, chargeID, piID, "needs_response", 150000, 1_700_000_100, &due, nil)
	postSignedWebhook(t, ctx, h, ev)

	var status string
	var orderNumber *string
	if err := pool.QueryRow(ctx, `
		SELECT d.status, o.order_number
		FROM payment_disputes d
		JOIN payments p ON p.id = d.payment_id
		JOIN orders o ON o.id = p.order_id
		WHERE d.provider_ref = $1`, disputeID).Scan(&status, &orderNumber); err != nil {
		t.Fatalf("read dispute: %v", err)
	}
	if status != "needs_response" || orderNumber == nil || *orderNumber != number {
		t.Fatalf("dispute status=%q order=%v, want needs_response on %q", status, orderNumber, number)
	}
}

func TestDisputeDuplicateEventIsIdempotent(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	h := disputeHandler(t, s)
	number, _ := order(t, 88000)
	session := "cs_dup_" + uuid.NewString()[:8]
	piID := "pi_dup_" + uuid.NewString()[:8]
	chargeID := "ch_dup_" + uuid.NewString()[:8]
	capturePaidOrder(t, ctx, s, h, number, session, piID, chargeID, 88000)

	eventID := "evt_dup_dispute"
	disputeID := "dp_dup_" + uuid.NewString()[:8]
	ev := disputePayload(eventID, disputeID, chargeID, piID, "needs_response", 88000, 1_700_000_200, nil, nil)
	postSignedWebhook(t, ctx, h, ev)
	postSignedWebhook(t, ctx, h, ev)

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payment_dispute_events WHERE provider_event_id = $1`, eventID).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 1 {
		t.Fatalf("duplicate dispute event recorded %d times, want 1", count)
	}
}

func TestDisputeLostThenWonAcceptsAppeal(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(disputeStoreRolePool(t))
	h := disputeHandler(t, s)
	number, _ := order(t, 120000)
	session := "cs_appeal_" + uuid.NewString()[:8]
	piID := "pi_appeal_" + uuid.NewString()[:8]
	chargeID := "ch_appeal_" + uuid.NewString()[:8]
	capturePaidOrder(t, ctx, s, h, number, session, piID, chargeID, 120000)
	disputeID := "dp_appeal_" + uuid.NewString()[:8]

	lost := disputePayload("evt_lost", disputeID, chargeID, piID, "lost", 120000, 1_700_000_300, nil, nil)
	lost["type"] = "charge.dispute.closed"
	postSignedWebhook(t, ctx, h, lost)

	assertDisputeStatus(t, disputeID, "lost", 1_700_000_300)
	for i, status := range []string{"under_review", "needs_response", "won"} {
		stale := disputePayload("evt_stale_"+uuid.NewString(), disputeID, chargeID, piID, status, 120000, 1_700_000_200+int64(i), nil, nil)
		stale["type"] = "charge.dispute.updated"
		postSignedWebhook(t, ctx, h, stale)
		assertDisputeStatus(t, disputeID, "lost", 1_700_000_300)
	}

	won := disputePayload("evt_won", disputeID, chargeID, piID, "won", 120000, 1_700_000_400, nil, nil)
	won["type"] = "charge.dispute.updated"
	postSignedWebhook(t, ctx, h, won)

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM payment_disputes WHERE provider_ref = $1`, disputeID).Scan(&status); err != nil {
		t.Fatalf("read dispute: %v", err)
	}
	if status != "won" {
		t.Fatalf("after appeal status = %q, want won", status)
	}
	assertDisputeStatus(t, disputeID, "won", 1_700_000_400)
	var history int
	if historyErr := pool.QueryRow(ctx, `SELECT count(*) FROM payment_dispute_events e JOIN payment_disputes d ON d.id = e.dispute_id WHERE d.provider_ref = $1`, disputeID).Scan(&history); historyErr != nil {
		t.Fatal(historyErr)
	}
	if history != 5 {
		t.Fatalf("retained history = %d, want all five provider observations", history)
	}
}

func TestDisputeFundsWithdrawnAndReinstated(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(disputeStoreRolePool(t))
	h := disputeHandler(t, s)
	number, _ := order(t, 99000)
	session := "cs_move_" + uuid.NewString()[:8]
	piID := "pi_move_" + uuid.NewString()[:8]
	chargeID := "ch_move_" + uuid.NewString()[:8]
	capturePaidOrder(t, ctx, s, h, number, session, piID, chargeID, 99000)
	disputeID := "dp_move_" + uuid.NewString()[:8]
	movements := []map[string]any{
		{"id": "txn_withdraw", "object": "balance_transaction", "amount": -99000},
		{"id": "txn_reinstate", "object": "balance_transaction", "amount": 49000},
	}
	ev := disputePayload("evt_move", disputeID, chargeID, piID, "won", 99000, 1_700_000_500, nil, movements)
	ev["type"] = "charge.dispute.updated"
	postSignedWebhook(t, ctx, h, ev)

	var withdrawn, reinstated int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(CASE WHEN m.kind = 'withdrawn' THEN m.amount_cents END), 0),
		       coalesce(sum(CASE WHEN m.kind = 'reinstated' THEN m.amount_cents END), 0)
		FROM payment_dispute_movements m
		JOIN payment_disputes d ON d.id = m.dispute_id
		WHERE d.provider_ref = $1`, disputeID).Scan(&withdrawn, &reinstated); err != nil {
		t.Fatalf("read movements: %v", err)
	}
	if withdrawn != 99000 || reinstated != 49000 {
		t.Fatalf("movements withdrawn=%d reinstated=%d, want 99000/49000", withdrawn, reinstated)
	}
	assertDisputeMovement(t, disputeID, "txn_withdraw", "withdrawn", 99000)
	assertDisputeMovement(t, disputeID, "txn_reinstate", "reinstated", 49000)
	// Different lifecycle events can repeat the same balance transaction.
	ev["id"] = "evt_move_duplicate_" + uuid.NewString()
	postSignedWebhook(t, ctx, h, ev)
	var count int
	if countErr := pool.QueryRow(ctx, `SELECT count(*) FROM payment_dispute_movements m JOIN payment_disputes d ON d.id = m.dispute_id WHERE d.provider_ref = $1`, disputeID).Scan(&count); countErr != nil {
		t.Fatal(countErr)
	}
	if count != 2 {
		t.Fatalf("movements after distinct-event replay = %d, want 2", count)
	}
}

func TestDisputeSeparateFundsEventsPersistMovements(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(disputeStoreRolePool(t))
	h := disputeHandler(t, s)
	number, _ := order(t, 99000)
	suffix := uuid.NewString()
	session, piID, chargeID := "cs_funds_"+suffix, "pi_funds_"+suffix, "ch_funds_"+suffix
	capturePaidOrder(t, ctx, s, h, number, session, piID, chargeID, 99000)
	disputeID := "dp_funds_" + suffix
	for _, tc := range []struct {
		eventType string
		status    string
		kind      string
		amount    int64
		seen      int64
	}{
		{"charge.dispute.funds_withdrawn", "lost", "withdrawn", -99000, 1_700_000_300},
		{"charge.dispute.funds_reinstated", "won", "reinstated", 49000, 1_700_000_400},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			eventID, transactionID := "evt_"+tc.kind+suffix, "txn_"+tc.kind+suffix
			movements := []map[string]any{{"id": transactionID, "object": "balance_transaction", "amount": tc.amount}}
			ev := disputePayload(eventID, disputeID, chargeID, piID, tc.status, 99000, tc.seen, nil, movements)
			ev["type"] = tc.eventType
			postSignedWebhook(t, ctx, h, ev)
			postSignedWebhook(t, ctx, h, ev)
			amount := tc.amount
			if amount < 0 {
				amount = -amount
			}
			assertDisputeMovement(t, disputeID, transactionID, tc.kind, amount)
			assertDisputeStatus(t, disputeID, tc.status, tc.seen)
			var processed bool
			if readErr := pool.QueryRow(ctx, `SELECT processed_at IS NOT NULL FROM payment_webhook_events WHERE event_id = $1`, eventID).Scan(&processed); readErr != nil {
				t.Fatal(readErr)
			}
			if !processed {
				t.Fatal("movement was not committed with the processed inbox event")
			}
		})
	}
	var orderNumber string
	var refunds int
	if readErr := pool.QueryRow(ctx, `
		SELECT o.order_number, (SELECT count(*) FROM refunds r WHERE r.payment_id = p.id)
		FROM payment_disputes d JOIN payments p ON p.id = d.payment_id JOIN orders o ON o.id = p.order_id
		WHERE d.provider_ref = $1`, disputeID).Scan(&orderNumber, &refunds); readErr != nil {
		t.Fatal(readErr)
	}
	if orderNumber != number || refunds != 0 {
		t.Fatalf("funds events attributed order=%q refunds=%d, want order=%q and no refund", orderNumber, refunds, number)
	}
}

func assertDisputeStatus(t *testing.T, disputeID, wantStatus string, wantSeen int64) {
	t.Helper()
	var status string
	var seen time.Time
	if readErr := pool.QueryRow(t.Context(), `SELECT status, provider_seen_at FROM payment_disputes WHERE provider_ref = $1`, disputeID).Scan(&status, &seen); readErr != nil {
		t.Fatal(readErr)
	}
	if status != wantStatus || seen.Unix() != wantSeen {
		t.Fatalf("dispute status=%s seen=%d, want %s at %d", status, seen.Unix(), wantStatus, wantSeen)
	}
}

func assertDisputeMovement(t *testing.T, disputeID, transactionID, wantKind string, wantAmount int64) {
	t.Helper()
	var kind string
	var amount int64
	if readErr := pool.QueryRow(t.Context(), `
		SELECT m.kind, m.amount_cents FROM payment_dispute_movements m
		JOIN payment_disputes d ON d.id = m.dispute_id
		WHERE d.provider_ref = $1 AND m.provider_ref = $2`, disputeID, transactionID).Scan(&kind, &amount); readErr != nil {
		t.Fatalf("read balance transaction %s: %v", transactionID, readErr)
	}
	if kind != wantKind || amount != wantAmount {
		t.Fatalf("balance transaction %s: kind=%s amount=%d, want %s %d", transactionID, kind, amount, wantKind, wantAmount)
	}
}

func disputeStoreRolePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg := pool.Config().Copy()
	cfg.MaxConns = 1
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, roleErr := conn.Exec(ctx, `SET ROLE store`)
		return roleErr
	}
	p, poolErr := pgxpool.NewWithConfig(t.Context(), cfg)
	if poolErr != nil {
		t.Fatal(poolErr)
	}
	t.Cleanup(p.Close)
	var role, superuser string
	if roleErr := p.QueryRow(t.Context(), `SELECT current_user, current_setting('is_superuser')`).Scan(&role, &superuser); roleErr != nil {
		t.Fatal(roleErr)
	}
	if role != "store" || superuser != "off" {
		t.Fatalf("dispute handler role=%s superuser=%s, want store/off", role, superuser)
	}
	return p
}

func TestUnattributedDisputeIsUnreconciled(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	h := disputeHandler(t, s)
	eventID := "evt_unattr_" + uuid.NewString()[:8]
	disputeID := "dp_unattr_" + uuid.NewString()[:8]
	ev := disputePayload(eventID, disputeID, "ch_unknown", "pi_unknown", "needs_response", 50000, 1_700_000_600, nil, nil)
	postSignedWebhook(t, ctx, h, ev)

	var reason *string
	if err := pool.QueryRow(ctx,
		`SELECT unreconciled FROM payment_webhook_events WHERE event_id = $1`, eventID).Scan(&reason); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if reason == nil || !strings.HasPrefix(*reason, "unattributed_dispute:") {
		t.Fatalf("unreconciled = %v, want unattributed_dispute prefix", reason)
	}
}

func TestOpenDisputeBlocksAnotherRefund(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	h := disputeHandler(t, s)
	number, _ := order(t, 75000)
	session := "cs_block_" + uuid.NewString()[:8]
	piID := "pi_block_" + uuid.NewString()[:8]
	chargeID := "ch_block_" + uuid.NewString()[:8]
	capturePaidOrder(t, ctx, s, h, number, session, piID, chargeID, 75000)
	disputeID := "dp_block_" + uuid.NewString()[:8]
	ev := disputePayload("evt_block", disputeID, chargeID, piID, "needs_response", 75000, 1_700_000_700, nil, nil)
	postSignedWebhook(t, ctx, h, ev)

	var paymentID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM payments WHERE provider_ref = $1`, session).Scan(&paymentID); err != nil {
		t.Fatalf("read payment: %v", err)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents, status)
		VALUES ($1, $2, 1000, 'pending')`, paymentID, "refund-block-"+uuid.NewString()[:8])
	if err == nil {
		t.Fatal("refund insert succeeded while dispute is open, want refusal")
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "refunds_blocked_by_open_dispute" {
		t.Fatalf("refund error = %v, want constraint refunds_blocked_by_open_dispute", err)
	}
}

func TestBackfillIgnoredDisputeEvent(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, _ := order(t, 66000)
	session := "cs_backfill_" + uuid.NewString()[:8]
	piID := "pi_backfill_" + uuid.NewString()[:8]
	chargeID := "ch_backfill_" + uuid.NewString()[:8]
	if err := s.OpenPayment(ctx, number, session, 66000); err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err := captureThroughWebhook(t, s, payment.Capture{
		SessionID: session, AmountRecv: 66000, PaymentIntentID: piID, ChargeID: chargeID,
	})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	eventID := "evt_ignored_dispute"
	disputeID := "dp_backfill_" + uuid.NewString()[:8]
	payload, err := json.Marshal(disputePayload(eventID, disputeID, chargeID, piID, "needs_response", 66000, 1_700_000_800, nil, nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_webhook_events (provider, event_id, type, object_ref, payload, processed_at)
		VALUES ('stripe', $1, 'charge.dispute.created', $2, $3, now())`,
		eventID, disputeID, payload); err != nil {
		t.Fatalf("seed ignored event: %v", err)
	}

	d, ok := payment.DisputeFrom(disputeFromPayload(t, payload))
	if !ok {
		t.Fatal("seed payload is not a readable dispute")
	}
	if err := s.ReconcileStoredDisputeEvent(ctx, eventID, d); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	var linked bool
	if err := pool.QueryRow(ctx, `
		SELECT payment_id IS NOT NULL FROM payment_disputes WHERE provider_ref = $1`, disputeID).Scan(&linked); err != nil {
		t.Fatalf("read dispute: %v", err)
	}
	if !linked {
		t.Fatal("backfilled dispute is still unattributed")
	}
}

func disputeFromPayload(t *testing.T, payload []byte) *stripe.Event {
	t.Helper()
	var wrapper struct {
		ID      string          `json:"id"`
		Type    string          `json:"type"`
		Created int64           `json:"created"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &wrapper); err != nil {
		t.Fatalf("unmarshal wrapper: %v", err)
	}
	ev := stripe.Event{ID: wrapper.ID, Type: stripe.EventType(wrapper.Type), Created: wrapper.Created}
	var data stripe.EventData
	if err := json.Unmarshal(wrapper.Data, &data); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	ev.Data = &data
	return &ev
}
