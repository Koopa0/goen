package payment

import (
	"encoding/json"
	"fmt"
	"testing"

	stripe "github.com/stripe/stripe-go/v86"
)

// TestAuditPostCaptureRoutingBoundary characterizes which post-capture Stripe
// events still record-and-ignore versus those with a business effect.
func TestAuditPostCaptureRoutingBoundary(t *testing.T) {
	const secret = "whsec_local_audit_fixture_only" //nolint:gosec // G101: test fixture
	gateway, err := NewGateway("sk_test_local_audit_fixture_only", secret, "http://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	for i, typ := range []string{
		"refund.created", "refund.updated", "refund.failed", "charge.refunded",
		"charge.dispute.created", "charge.dispute.updated", "charge.dispute.closed",
	} {
		t.Run(typ, func(t *testing.T) {
			obj := map[string]any{
				"id": fmt.Sprintf("re_audit%d", i), "object": "refund",
				"payment_intent": "pi_audit", "amount": 100, "currency": "twd", "status": "succeeded",
			}
			if typ == "charge.refunded" {
				obj["id"] = "ch_audit"
				obj["object"] = "charge"
			}
			if i >= 4 {
				obj = map[string]any{
					"id": "dp_audit", "object": "dispute", "status": "needs_response",
					"amount": 100, "currency": "twd",
					"charge":         map[string]any{"id": "ch_audit", "object": "charge"},
					"payment_intent": map[string]any{"id": "pi_audit", "object": "payment_intent"},
				}
			}
			raw, err := json.Marshal(map[string]any{
				"id": fmt.Sprintf("evt_audit%d", i), "object": "event",
				"api_version": stripe.APIVersion, "type": typ,
				"created": 1_700_000_000,
				"data":    map[string]any{"object": obj},
			})
			if err != nil {
				t.Fatal(err)
			}
			signed := stripe.GenerateTestSignedPayload(&stripe.UnsignedPayload{
				Payload: raw, Secret: secret,
			})
			ev, err := gateway.VerifyWebhook(signed.Payload, signed.Header)
			if err != nil {
				t.Fatal(err)
			}
			_, capture := CaptureFrom(&ev)
			_, abandoned := AbandonedSessionFrom(&ev)
			_, unsettled := UnsettledSessionFrom(&ev)
			_, isDispute := DisputeFrom(&ev)
			state := classifyWebhook(&ev, capture || abandoned || unsettled || isDispute)
			outcome := &webhookOutcome{
				event: &ev, readState: state,
				isAbandoned: abandoned, isCapture: capture, isUnsettled: unsettled, isDispute: isDispute,
			}
			ignored := state == webhookReadIgnored
			hasEffect := outcome.apply() != nil
			wantIgnored := i < 4
			wantEffect := i >= 4
			if ignored != wantIgnored || hasEffect != wantEffect {
				t.Fatalf("routing changed: ignored=%v effect=%v; want ignored=%v effect=%v",
					ignored, hasEffect, wantIgnored, wantEffect)
			}
		})
	}
}
