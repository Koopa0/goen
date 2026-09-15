package payment

import (
	"encoding/json"
	"fmt"
	"testing"

	stripe "github.com/stripe/stripe-go/v86"
)

// Characterizes routing for post-capture Stripe events. Dispute types stay
// ignored until #339; refund types are reconciled here per #338.
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
				obj["id"] = "dp_audit"
				obj["object"] = "dispute"
				obj["status"] = "needs_response"
				obj["charge"] = "ch_audit"
			}
			raw, err := json.Marshal(map[string]any{
				"id": fmt.Sprintf("evt_audit%d", i), "object": "event",
				"api_version": stripe.APIVersion, "type": typ,
				"data": map[string]any{"object": obj},
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
			_, refund := RefundFrom(&ev)
			_, charge := RefundsFromCharge(&ev)
			understood := capture || abandoned || unsettled || refund || charge
			state := classifyWebhook(&ev, understood)
			outcome := &webhookOutcome{
				event: &ev, readState: state,
				isCapture: capture, isAbandoned: abandoned, isUnsettled: unsettled,
				isRefund: refund, isChargeRefunded: charge,
			}
			ignored := state == webhookReadIgnored
			hasEffect := outcome.apply() != nil
			wantIgnored := i >= 4
			wantEffect := i < 4
			if ignored != wantIgnored || hasEffect != wantEffect {
				t.Fatalf("routing changed: ignored=%v effect=%v; want ignored=%v effect=%v",
					ignored, hasEffect, wantIgnored, wantEffect)
			}
		})
	}
}
