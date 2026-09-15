//go:build integration

package payment_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/payment"
)

func refundWebhookEvent(eventID, refundID, intentID string, amount int64, status, key string) map[string]any {
	obj := map[string]any{
		"id": refundID, "object": "refund", "amount": amount, "currency": "twd",
		"status": status, "created": 1_700_000_000,
		"payment_intent": map[string]any{"id": intentID, "object": "payment_intent"},
	}
	if key != "" {
		obj["metadata"] = map[string]string{"goen_request_key": key}
	}
	return map[string]any{
		"id": eventID, "object": "event", "type": "refund.updated",
		"data": map[string]any{"object": obj},
	}
}

func postRefundWebhook(t *testing.T, event map[string]any) {
	t.Helper()
	ctx := t.Context()
	body, header := signed(t, event)
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/webhooks/stripe", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", header)
	res := httptest.NewRecorder()
	payment.NewHandler(payment.NewStore(pool), enabledGateway(t), alwaysPlacedHere{},
		slog.New(slog.DiscardHandler), false).Webhook(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("Webhook() status = %d, want 200", res.Code)
	}
}

func TestExternalDashboardRefundIsAttributedToOrder(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, _ := order(t, 120000)
	session := "cs_ext_refund_" + uuid.NewString()[:12]
	intent := "pi_ext_refund_" + uuid.NewString()[:12]
	if err := s.OpenPayment(ctx, number, session, 120000); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := captureThroughWebhook(t, s, payment.Capture{
		SessionID: session, AmountRecv: 120000,
	}); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`SELECT record_payment_intent_link($1, $2)`, session, intent); err != nil {
		t.Fatalf("link intent: %v", err)
	}

	eventID := "evt_ext_" + uuid.NewString()[:12]
	refundID := "re_ext_" + uuid.NewString()[:12]
	postRefundWebhook(t, refundWebhookEvent(eventID, refundID, intent, 120000, "succeeded", ""))

	var allocation string
	var orderNumber string
	if err := pool.QueryRow(ctx, `
		SELECT srf.allocation, o.order_number
		FROM stripe_refund_facts srf
		JOIN orders o ON o.id = srf.order_id
		WHERE srf.provider_ref = $1`, refundID).Scan(&allocation, &orderNumber); err != nil {
		t.Fatalf("read provider refund fact: %v", err)
	}
	if allocation != "external" || orderNumber != number {
		t.Fatalf("fact = %q/%q, want external/%s", allocation, orderNumber, number)
	}
}

func TestLocalPendingRefundReceivesProviderUpdate(t *testing.T) {
	ctx := t.Context()
	number, orderID := order(t, 80000)
	session := "cs_local_refund_" + uuid.NewString()[:12]
	intent := "pi_local_refund_" + uuid.NewString()[:12]
	if err := payment.NewStore(pool).OpenPayment(ctx, number, session, 80000); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := captureThroughWebhook(t, payment.NewStore(pool), payment.Capture{
		SessionID: session, AmountRecv: 80000,
	}); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`SELECT record_payment_intent_link($1, $2)`, session, intent); err != nil {
		t.Fatalf("link intent: %v", err)
	}

	var paymentID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM payments WHERE provider_ref = $1`, session).Scan(&paymentID); err != nil {
		t.Fatalf("read payment: %v", err)
	}
	requestKey := "return:" + uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents, status)
		VALUES ($1, $2, 80000, 'pending')`, paymentID, requestKey); err != nil {
		t.Fatalf("insert pending refund: %v", err)
	}

	refundID := "re_local_" + uuid.NewString()[:12]
	postRefundWebhook(t, refundWebhookEvent(
		"evt_local_"+uuid.NewString()[:8], refundID, intent, 80000, "succeeded", requestKey))

	var status, providerRef string
	if err := pool.QueryRow(ctx,
		`SELECT status, provider_ref FROM refunds WHERE request_key = $1`, requestKey).
		Scan(&status, &providerRef); err != nil {
		t.Fatalf("read refund: %v", err)
	}
	if status != "succeeded" || providerRef != refundID {
		t.Fatalf("refund = %q/%q, want succeeded/%s", status, providerRef, refundID)
	}
	_ = orderID
}

func TestReplayedRefundWebhookDoesNotDuplicateFacts(t *testing.T) {
	ctx := t.Context()
	number, _ := order(t, 50000)
	session := "cs_dedupe_" + uuid.NewString()[:12]
	intent := "pi_dedupe_" + uuid.NewString()[:12]
	s := payment.NewStore(pool)
	if err := s.OpenPayment(ctx, number, session, 50000); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := captureThroughWebhook(t, s, payment.Capture{SessionID: session, AmountRecv: 50000}); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT record_payment_intent_link($1, $2)`, session, intent); err != nil {
		t.Fatalf("link: %v", err)
	}

	eventID := "evt_dedupe_" + uuid.NewString()[:8]
	refundID := "re_dedupe_" + uuid.NewString()[:8]
	ev := refundWebhookEvent(eventID, refundID, intent, 50000, "succeeded", "")
	postRefundWebhook(t, ev)
	postRefundWebhook(t, ev)

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM stripe_refund_facts WHERE provider_ref = $1`, refundID).
		Scan(&count); err != nil {
		t.Fatalf("count facts: %v", err)
	}
	if count != 1 {
		t.Fatalf("fact rows = %d, want 1", count)
	}
}

func TestIgnoredRefundEventCanBeBackfilled(t *testing.T) {
	ctx := t.Context()
	eventID := "evt_backfill_" + uuid.NewString()[:8]
	refundID := "re_backfill_" + uuid.NewString()[:8]
	intent := "pi_backfill_" + uuid.NewString()[:8]
	payload := fmt.Sprintf(`{"id":"%s","object":"event","type":"refund.created","data":{"object":{
		"id":"%s","object":"refund","amount":10000,"currency":"twd","status":"succeeded",
		"created":1700000000,"payment_intent":{"id":"%s","object":"payment_intent"}
	}}}`, eventID, refundID, intent)
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_webhook_events
		    (provider, event_id, type, object_ref, payload, processed_at)
		VALUES ('stripe', $1, 'refund.created', $2, $3::jsonb, now())`,
		eventID, refundID, payload); err != nil {
		t.Fatalf("seed ignored event: %v", err)
	}

	applied, err := payment.NewStore(pool).BackfillIgnoredRefundWebhooks(ctx, 10)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if applied != 1 {
		t.Fatalf("backfill applied = %d, want 1", applied)
	}
	var reconciled bool
	if err := pool.QueryRow(ctx, `
		SELECT refund_reconciled_at IS NOT NULL FROM payment_webhook_events
		WHERE event_id = $1`, eventID).Scan(&reconciled); err != nil || !reconciled {
		t.Fatalf("backfilled event reconciled = %v, err = %v", reconciled, err)
	}
}
