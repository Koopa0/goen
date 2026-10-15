package payment

import (
	"os"
	"strings"
	"testing"

	stripe "github.com/stripe/stripe-go/v86"
)

// A Stripe endpoint is subscribed to events by hand, and Stripe never sends one
// nobody subscribed to: an event goen acts on but the setup does not name is a
// branch that silently never runs.
func TestEveryEventGoenActsOnIsInTheSetup(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatalf("read .env.example: %v", err)
	}
	acted := []stripe.EventType{stripe.EventTypeRefundFailed}
	for _, events := range []map[stripe.EventType]bool{captureEvents, abandonedEvents} {
		for ev := range events {
			acted = append(acted, ev)
		}
	}
	for _, ev := range acted {
		if !actionable(&stripe.Event{Type: ev}) {
			t.Errorf("%s is listed here but goen does not act on it", ev)
		}
		if !strings.Contains(string(raw), string(ev)) {
			t.Errorf(".env.example does not name %s among the events to subscribe", ev)
		}
	}
}
