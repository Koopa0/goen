package payment_test

import (
	"encoding/json"
	"testing"

	stripe "github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/payment"
)

func refundEvent(typ, id, intentID string, amount int64, status, key string) stripe.Event {
	obj := map[string]any{
		"id": id, "object": "refund", "amount": amount, "currency": "twd",
		"status": status, "payment_intent": map[string]any{"id": intentID, "object": "payment_intent"},
	}
	if key != "" {
		obj["metadata"] = map[string]string{"goen_request_key": key}
	}
	raw, _ := json.Marshal(map[string]any{
		"id": "evt_" + id, "object": "event", "type": typ,
		"data": map[string]any{"object": obj},
	})
	var ev stripe.Event
	_ = json.Unmarshal(raw, &ev)
	return ev
}

func TestRefundFromReadsProviderFacts(t *testing.T) {
	ev := refundEvent("refund.created", "re_test_1", "pi_test_1", 50000, "succeeded", "return:abc")
	got, ok := payment.RefundFrom(&ev)
	if !ok {
		t.Fatal("refund.created was not understood")
	}
	if got.ProviderRef != "re_test_1" || got.PaymentIntentRef != "pi_test_1" ||
		got.AmountCents != 50000 || got.Status != "succeeded" || got.RequestKey != "return:abc" {
		t.Fatalf("RefundFrom = %+v", got)
	}
}

func TestRefundEventsAreActionable(t *testing.T) {
	for _, typ := range []string{"refund.created", "refund.updated", "refund.failed"} {
		ev := refundEvent(typ, "re_"+typ, "pi_x", 100, "pending", "")
		_, ok := payment.RefundFrom(&ev)
		if !ok {
			t.Fatalf("%s was not readable", typ)
		}
	}
}
