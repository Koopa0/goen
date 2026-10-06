package payment

import (
	"encoding/json"
	"strings"
	"testing"

	stripe "github.com/stripe/stripe-go/v86"
)

func TestEveryActionableWebhookTypeHasOneReadState(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		event      *stripe.Event
		understood bool
		want       webhookReadState
	}{
		{name: "nil", want: webhookReadIgnored},
		{name: "unrelated", event: &stripe.Event{Type: "customer.created"}, want: webhookReadIgnored},
		{name: "completed unreadable", event: &stripe.Event{Type: "checkout.session.completed"}, want: webhookReadUnreadable},
		{name: "async success unreadable", event: &stripe.Event{Type: "checkout.session.async_payment_succeeded"}, want: webhookReadUnreadable},
		{name: "expired unreadable", event: &stripe.Event{Type: "checkout.session.expired"}, want: webhookReadUnreadable},
		{name: "async failure unreadable", event: &stripe.Event{Type: "checkout.session.async_payment_failed"}, want: webhookReadUnreadable},
		{name: "completed understood", event: &stripe.Event{Type: "checkout.session.completed"}, understood: true, want: webhookReadUnderstood},
		{name: "refund failure unreadable", event: &stripe.Event{Type: "refund.failed"}, want: webhookReadUnreadable},
		{name: "refund failure understood", event: &stripe.Event{Type: "refund.failed"}, understood: true, want: webhookReadUnderstood},
		{name: "refund update", event: &stripe.Event{Type: "refund.updated"}, want: webhookReadIgnored},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyWebhook(tt.event, tt.understood); got != tt.want {
				t.Errorf("classifyWebhook(%v, %v) = %v, want %v", tt.event, tt.understood, got, tt.want)
			}
		})
	}
}

func TestARefundFailureIsReadFromItsRefund(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		typ    string
		object map[string]any
		want   refundFailure
		ok     bool
	}{
		{
			name:   "a failed card refund",
			typ:    "refund.failed",
			object: map[string]any{"id": "re_3Q1abc", "object": "refund", "status": "failed", "failure_reason": "lost_or_stolen_card"},
			want:   refundFailure{refundID: "re_3Q1abc", reason: "lost_or_stolen_card"},
			ok:     true,
		},
		{
			name:   "no failure reason",
			typ:    "refund.failed",
			object: map[string]any{"id": "re_3Q1abc", "object": "refund", "status": "failed"},
			want:   refundFailure{refundID: "re_3Q1abc"},
			ok:     true,
		},
		{
			name:   "a reason that is not a plain code",
			typ:    "refund.failed",
			object: map[string]any{"id": "re_3Q1abc", "object": "refund", "failure_reason": "Lost card\nlevel=INFO"},
			want:   refundFailure{refundID: "re_3Q1abc"},
			ok:     true,
		},
		{
			name:   "a reason longer than a code",
			typ:    "refund.failed",
			object: map[string]any{"id": "re_3Q1abc", "object": "refund", "failure_reason": strings.Repeat("a", 65)},
			want:   refundFailure{refundID: "re_3Q1abc"},
			ok:     true,
		},
		{
			name:   "no refund id",
			typ:    "refund.failed",
			object: map[string]any{"object": "refund", "failure_reason": "unknown"},
		},
		{
			name:   "an id with whitespace",
			typ:    "refund.failed",
			object: map[string]any{"id": "re_3Q1 abc", "object": "refund"},
		},
		{
			name:   "another refund event",
			typ:    "refund.updated",
			object: map[string]any{"id": "re_3Q1abc", "object": "refund", "status": "failed"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(map[string]any{
				"id": "evt_refund", "object": "event", "type": tt.typ,
				"data": map[string]any{"object": tt.object},
			})
			if err != nil {
				t.Fatal(err)
			}
			var ev stripe.Event
			if err := json.Unmarshal(raw, &ev); err != nil {
				t.Fatal(err)
			}
			got, ok := refundFailureFrom(&ev)
			if got != tt.want || ok != tt.ok {
				t.Errorf("refundFailureFrom(%s %v) = %+v, %v; want %+v, %v",
					tt.typ, tt.object, got, ok, tt.want, tt.ok)
			}
		})
	}
}
