package payment

import (
	"testing"

	stripe "github.com/stripe/stripe-go/v86"
)

func TestEveryCheckoutWebhookTypeHasOneReadState(t *testing.T) {
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
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyWebhook(tt.event, tt.understood); got != tt.want {
				t.Errorf("classifyWebhook(%v, %v) = %v, want %v", tt.event, tt.understood, got, tt.want)
			}
		})
	}
}
