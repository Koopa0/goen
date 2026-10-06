//go:build integration

package payment_test

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	stripe "github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/orderaccess"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

// goenRefund is a refund goen issued against a captured payment, standing in
// the given status with a Stripe reference.
type goenRefund struct {
	orderNumber string
	requestKey  string
	providerRef string
	cents       int64
}

func refundIssuedByGoen(t *testing.T, status string) goenRefund {
	t.Helper()
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, _, session := openOrder(t, s, 88000, "refundfail")
	if _, err := captureThroughWebhook(t, s, payment.Capture{SessionID: session, AmountRecv: 88000}); err != nil {
		t.Fatalf("capture the payment the refund is against: %v", err)
	}
	r := goenRefund{
		orderNumber: number,
		requestKey:  "refund-failed-test:" + uuid.NewString(),
		providerRef: "re_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20],
		cents:       30000,
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents, status, provider_ref, succeeded_at)
		SELECT p.id, $2, $3, $4::text, $5,
		       CASE WHEN $4::text = 'succeeded' THEN now() END
		FROM payments p WHERE p.provider_ref = $1`,
		session, r.requestKey, r.cents, status, r.providerRef); err != nil {
		t.Fatalf("record goen's %s refund: %v", status, err)
	}
	return r
}

func refundFailedEvent(eventID, refundID string, extra map[string]any) map[string]any {
	object := map[string]any{
		"id": refundID, "object": "refund", "status": "failed",
		"amount": 30000, "currency": "twd", "failure_reason": "lost_or_stolen_card",
	}
	for k, v := range extra {
		object[k] = v
	}
	return map[string]any{
		"id": eventID, "object": "event", "api_version": stripe.APIVersion,
		"type": "refund.failed", "data": map[string]any{"object": object},
	}
}

// refundRowState is everything about goen's refund row a webhook must leave alone.
type refundRowState struct {
	status      string
	succeededAt string
	failedAt    string
}

func refundRow(t *testing.T, ctx context.Context, providerRef string) refundRowState {
	t.Helper()
	var r refundRowState
	if err := pool.QueryRow(ctx, `
		SELECT status, coalesce(succeeded_at::text, ''), coalesce(failed_at::text, '')
		FROM refunds WHERE provider_ref = $1`,
		providerRef).Scan(&r.status, &r.succeededAt, &r.failedAt); err != nil {
		t.Fatalf("read refund %s: %v", providerRef, err)
	}
	return r
}

// openAlarms counts the payment alarms naming this Stripe object that no one
// has released.
func openAlarms(t *testing.T, ctx context.Context, objectRef string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM payment_webhook_events
		WHERE object_ref = $1 AND unreconciled IS NOT NULL AND reconciled_at IS NULL`,
		objectRef).Scan(&n); err != nil {
		t.Fatalf("count open alarms for %s: %v", objectRef, err)
	}
	return n
}

func healthEvent(t *testing.T, ctx context.Context, eventID string) (admin.UnreconciledEvent, bool) {
	t.Helper()
	page, err := health.NewStore(pool).WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("read health: %v", err)
	}
	for _, e := range page.UnreconciledEvents {
		if e.EventID == eventID {
			return e, true
		}
	}
	return admin.UnreconciledEvent{}, false
}

// storeWebhook is the webhook as production runs it, under the store role,
// which cannot write refunds.
func storeWebhook(t *testing.T) *payment.Handler {
	t.Helper()
	return payment.NewHandler(payment.NewStore(rolePool(t, "store")), enabledGateway(t),
		orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler))
}

// TestARefundStripeFailedAfterGoenRecordedItSucceededAlarmsOnce holds the one
// case goen must surface: the customer was told the money went back, the bank
// returned it, and only a person can repay them.
func TestARefundStripeFailedAfterGoenRecordedItSucceededAlarmsOnce(t *testing.T) {
	ctx := t.Context()
	h := storeWebhook(t)
	refund := refundIssuedByGoen(t, "succeeded")
	before := refundRow(t, ctx, refund.providerRef)

	eventID := "evt_refund_failed_" + uuid.NewString()[:12]
	body, header := signed(t, refundFailedEvent(eventID, refund.providerRef, nil))
	if w := deliver(t, h, body, header); w.Code != http.StatusOK {
		t.Fatalf("Webhook() status = %d, want 200", w.Code)
	}

	var processed bool
	var reason *string
	if err := pool.QueryRow(ctx, `
		SELECT processed_at IS NOT NULL, unreconciled FROM payment_webhook_events
		WHERE event_id = $1`, eventID).Scan(&processed, &reason); err != nil {
		t.Fatalf("read the recorded event: %v", err)
	}
	if !processed {
		t.Error("the refund failure was not marked processed, so Stripe retries it")
	}
	if reason == nil || *reason != "refund_failed: lost_or_stolen_card" {
		t.Fatalf("unreconciled = %v, want the refund_failed cause and Stripe's code only", reason)
	}
	if got := refundRow(t, ctx, refund.providerRef); got != before {
		t.Errorf("goen's refund row moved from %+v to %+v; the webhook must not rewrite it", before, got)
	}
	listed, ok := healthEvent(t, ctx, eventID)
	if !ok {
		t.Fatalf("health does not list the failed refund %s", eventID)
	}
	if listed.RefundOrderNumber != refund.orderNumber || listed.RefundCents != refund.cents {
		t.Errorf("health lists order %q for %d cents, want order %q for %d cents",
			listed.RefundOrderNumber, listed.RefundCents, refund.orderNumber, refund.cents)
	}

	if w := deliver(t, h, body, header); w.Code != http.StatusOK {
		t.Fatalf("redelivery status = %d, want 200", w.Code)
	}
	if n := openAlarms(t, ctx, refund.providerRef); n != 1 {
		t.Fatalf("after a redelivery %d alarms are open for %s, want 1", n, refund.providerRef)
	}

	var paymentStatus string
	var captured int64
	readPayment := func() {
		t.Helper()
		if err := pool.QueryRow(ctx, `
			SELECT p.status, p.captured_amount_cents FROM payments p
			JOIN refunds r ON r.payment_id = p.id WHERE r.provider_ref = $1`,
			refund.providerRef).Scan(&paymentStatus, &captured); err != nil {
			t.Fatalf("read the refunded payment: %v", err)
		}
	}
	readPayment()
	paidStatus, paidCents := paymentStatus, captured

	staff, _ := admintest.StaffContext(t, pool)
	if err := health.NewStore(adminRolePool(t)).
		ReleasePaymentEventAfterRefundOrAccounting(staff, eventID); err != nil {
		t.Fatalf("release the failed refund's alarm: %v", err)
	}
	if _, ok := healthEvent(t, ctx, eventID); ok {
		t.Error("a released refund failure is still on /admin/health")
	}
	readPayment()
	if paymentStatus != paidStatus || captured != paidCents {
		t.Errorf("release moved the payment from %s/%d to %s/%d, want it untouched",
			paidStatus, paidCents, paymentStatus, captured)
	}
	if got := refundRow(t, ctx, refund.providerRef); got != before {
		t.Errorf("release moved goen's refund row from %+v to %+v", before, got)
	}

	if w := deliver(t, h, body, header); w.Code != http.StatusOK {
		t.Fatalf("redelivery after release status = %d, want 200", w.Code)
	}
	if n := openAlarms(t, ctx, refund.providerRef); n != 0 {
		t.Errorf("a redelivery after release reopened %d alarms", n)
	}
	if n := eventRows(t, ctx, eventID); n != 1 {
		t.Errorf("the event is recorded %d times, want once", n)
	}
}

// TestARefundFailureRaisesNoAlarmUnlessGoenRecordedTheRefundSucceeded holds
// the two failures goen records and leaves: a refund goen still lists as open,
// and one goen cannot attribute by its own provider reference, however much
// the event's metadata names one of goen's refunds.
func TestARefundFailureRaisesNoAlarmUnlessGoenRecordedTheRefundSucceeded(t *testing.T) {
	tests := []struct {
		name   string
		status string
		event  func(r goenRefund) (refundID string, extra map[string]any)
	}{
		{
			name:   "goen's refund is still pending",
			status: "pending",
			event: func(r goenRefund) (string, map[string]any) {
				return r.providerRef, nil
			},
		},
		{
			name:   "a refund goen did not issue names a succeeded one of goen's",
			status: "succeeded",
			event: func(r goenRefund) (string, map[string]any) {
				return "re_unknown_" + uuid.NewString()[:12], map[string]any{
					"metadata":       map[string]any{"goen_request_key": r.requestKey},
					"payment_intent": "pi_" + uuid.NewString()[:12],
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			h := storeWebhook(t)
			refund := refundIssuedByGoen(t, tt.status)
			before := refundRow(t, ctx, refund.providerRef)
			refundID, extra := tt.event(refund)

			eventID := "evt_refund_failed_" + uuid.NewString()[:12]
			body, header := signed(t, refundFailedEvent(eventID, refundID, extra))
			if w := deliver(t, h, body, header); w.Code != http.StatusOK {
				t.Fatalf("Webhook() status = %d, want 200", w.Code)
			}
			var processed bool
			var reason *string
			if err := pool.QueryRow(ctx, `
				SELECT processed_at IS NOT NULL, unreconciled FROM payment_webhook_events
				WHERE event_id = $1`, eventID).Scan(&processed, &reason); err != nil {
				t.Fatalf("read the recorded event: %v", err)
			}
			if !processed {
				t.Error("the refund failure was not marked processed")
			}
			if reason != nil {
				t.Errorf("the refund failure was flagged %q, want it recorded without an alarm", *reason)
			}
			if got := refundRow(t, ctx, refund.providerRef); got != before {
				t.Errorf("goen's refund row moved from %+v to %+v", before, got)
			}
		})
	}
}
