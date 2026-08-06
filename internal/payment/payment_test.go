package payment_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	stripe "github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/loyalty"
	"github.com/koopa0/goen/internal/payment"
)

// A signing secret for the tests only; it authenticates nothing that exists.
const testWebhookSecret = "whsec_thisisatestsecretforgoenonly" //nolint:gosec // G101: test fixture

// signed produces a webhook body and header exactly as Stripe would sign them.
func signed(t *testing.T, body any) (payload []byte, header string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	p := stripe.GenerateTestSignedPayload(&stripe.UnsignedPayload{
		Payload: raw, Secret: testWebhookSecret,
	})
	return p.Payload, p.Header
}

// sessionEvent is a checkout.session.completed as Stripe sends it.
func sessionEvent(id, sessionID, paymentStatus string, amount int64) map[string]any {
	return map[string]any{
		"id":          id,
		"object":      "event",
		"api_version": stripe.APIVersion,
		"type":        "checkout.session.completed",
		"data": map[string]any{
			"object": map[string]any{
				"id":             sessionID,
				"object":         "checkout.session",
				"payment_status": paymentStatus,
				"amount_total":   amount,
				"currency":       "twd",
			},
		},
	}
}

func enabledGateway(t *testing.T) *payment.Gateway {
	t.Helper()
	g, err := payment.NewGateway("sk_test_notused", testWebhookSecret, "https://goen.example")
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	return g
}

// TestWebhookRejectsAForgedSignature proves an unsigned or mis-signed payload is
// refused. This is the whole security boundary of the
// endpoint that marks orders paid. Without it anyone who knows an order number
// can post themselves a free order.
func TestWebhookRejectsAForgedSignature(t *testing.T) {
	g := enabledGateway(t)
	body, header := signed(t, sessionEvent("evt_1", "cs_test_1", "paid", 199900))

	tests := []struct {
		name    string
		payload []byte
		header  string
	}{
		{"no signature at all", body, ""},
		{"signature from another secret", body, otherSecretHeader(t, body)},
		{"body changed after signing", append(body[:len(body)-1], []byte(`,"x":1}`)...), header},
		{"header is nonsense", body, "t=1,v1=deadbeef"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := g.VerifyWebhook(tt.payload, tt.header)
			if err == nil {
				t.Fatal("verification accepted a payload Stripe did not sign")
			}
			if !errors.Is(err, payment.ErrBadSignature) {
				t.Errorf("error is %v, want ErrBadSignature so the handler answers 400", err)
			}
		})
	}
}

func otherSecretHeader(t *testing.T, body []byte) string {
	t.Helper()
	p := stripe.GenerateTestSignedPayload(&stripe.UnsignedPayload{
		Payload: body, Secret: "whsec_adifferentsecretentirely",
	})
	return p.Header
}

// TestWebhookAcceptsAGenuineSignature is the other half: the guard must let the
// real thing through, or the first test would pass with verification hard-wired
// to fail.
func TestWebhookAcceptsAGenuineSignature(t *testing.T) {
	g := enabledGateway(t)
	body, header := signed(t, sessionEvent("evt_ok", "cs_test_ok", "paid", 199900))

	ev, err := g.VerifyWebhook(body, header)
	if err != nil {
		t.Fatalf("rejected a payload it signed itself: %v", err)
	}
	if ev.ID != "evt_ok" {
		t.Errorf("event id is %q, want evt_ok", ev.ID)
	}
}

// typed relabels an event. The payload of every checkout.session.* event is a
// checkout session, so one fixture serves them all.
func typed(ev map[string]any, eventType string) map[string]any {
	ev["type"] = eventType
	return ev
}

// TestOnlyAPaidSessionIsACapture proves an unpaid session is not treated as
// money received, and that the paid one of EACH event type is.
// `checkout.session.completed` fires for sessions that were never paid — an
// asynchronous payment method still processing, for instance. Acting on the
// event type alone marks those orders paid for money that has not arrived.
//
// The asynchronous rows are the defect this test used to describe and not cover.
// Dynamic payment methods are deliberately on, so a delayed method's `completed`
// arrives with payment_status `unpaid` — correctly refused below, and this test's
// own comment named that case. The success that FOLLOWS is
// `checkout.session.async_payment_succeeded`, which nothing accepted: the
// customer paid and the order stayed unpaid forever. It belongs here expecting
// true, and NOT in TestOtherEventTypesAreNotCaptures, which is where the instinct
// to "add the new event type" would put it and lock the bug in.
func TestOnlyAPaidSessionIsACapture(t *testing.T) {
	g := enabledGateway(t)

	tests := []struct {
		name      string
		event     map[string]any
		isCapture bool
	}{
		{"paid", sessionEvent("e1", "cs_1", "paid", 199900), true},
		{"unpaid", sessionEvent("e2", "cs_2", "unpaid", 199900), false},
		{"still processing", sessionEvent("e3", "cs_3", "no_payment_required", 199900), false},
		{"zero amount", sessionEvent("e4", "cs_4", "paid", 0), false},
		{
			name:      "asynchronous method that cleared",
			event:     typed(sessionEvent("e5", "cs_5", "paid", 199900), "checkout.session.async_payment_succeeded"),
			isCapture: true,
		},
		{
			name:      "asynchronous method still unpaid",
			event:     typed(sessionEvent("e6", "cs_6", "unpaid", 199900), "checkout.session.async_payment_succeeded"),
			isCapture: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, header := signed(t, tt.event)
			ev, err := g.VerifyWebhook(body, header)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			_, got := payment.CaptureFrom(&ev)
			if got != tt.isCapture {
				t.Errorf("CaptureFrom says %v, want %v", got, tt.isCapture)
			}
		})
	}
}

// TestASessionThatEndsWithNoMoneyIsAbandoned holds the other half of the
// asynchronous pair.
//
// A delayed method that does not clear arrives as
// `checkout.session.async_payment_failed`, and the payment row goen opened has
// to stop saying it is waiting for something — the same job
// `checkout.session.expired` does, so it goes to the same place. Nothing routed
// it anywhere, so the row sat at requires_payment for ever and reconciliation
// could not tell a dead checkout from one still in flight.
//
// A capture event is in the table because these two readers must not overlap:
// one of them cancels a payment and the other posts money to it.
func TestASessionThatEndsWithNoMoneyIsAbandoned(t *testing.T) {
	g := enabledGateway(t)

	tests := []struct {
		name        string
		eventType   string
		isAbandoned bool
	}{
		{"expired", "checkout.session.expired", true},
		{"asynchronous method failed", "checkout.session.async_payment_failed", true},
		{"completed", "checkout.session.completed", false},
		{"asynchronous method cleared", "checkout.session.async_payment_succeeded", false},
		{"an intent event", "payment_intent.payment_failed", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, header := signed(t, typed(sessionEvent("evt_ab", "cs_ab", "unpaid", 199900), tt.eventType))
			ev, err := g.VerifyWebhook(body, header)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			session, got := payment.AbandonedSessionFrom(&ev)
			if got != tt.isAbandoned {
				t.Errorf("AbandonedSessionFrom(%s) says %v, want %v", tt.eventType, got, tt.isAbandoned)
			}
			if got && session != "cs_ab" {
				t.Errorf("AbandonedSessionFrom(%s) named session %q, want cs_ab", tt.eventType, session)
			}
		})
	}
}

// TestTheSessionKeyFollowsTheOrderTheAmountAndTheAttempt holds the second line of
// defence against one order being charged twice.
//
// The key is what collapses two concurrent POSTs — both reading "no live
// session" before either has written its payment row — into ONE session at
// Stripe. A key that varied per call (a nonce, a timestamp) would look like an
// idempotency key and do nothing at all, which is the failure this locks: the
// same three inputs must produce the same string every time.
//
// It must also MOVE when any of the three moves. The amount, because a session
// for a total the order no longer owes must not be handed back; the attempt,
// because a Stripe idempotency key is honoured for 24 hours and a customer whose
// first session died would otherwise be given the dead one back and be unable to
// pay at all.
//
// The expected values are written out rather than computed, so the test is a
// statement about the wire format and not a restatement of the function.
func TestTheSessionKeyFollowsTheOrderTheAmountAndTheAttempt(t *testing.T) {
	tests := []struct {
		name    string
		number  string
		owed    int64
		attempt int32
		want    string
	}{
		{"first attempt", "GO-20260804-0007", 199900, 0, "goen-pay:GO-20260804-0007:199900:0"},
		{"another order", "GO-20260804-0008", 199900, 0, "goen-pay:GO-20260804-0008:199900:0"},
		{"the order owes less", "GO-20260804-0007", 169900, 0, "goen-pay:GO-20260804-0007:169900:0"},
		{"a second attempt", "GO-20260804-0007", 199900, 1, "goen-pay:GO-20260804-0007:199900:1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := payment.SessionKey(tt.number, tt.owed, tt.attempt)
			if got != tt.want {
				t.Errorf("SessionKey(%q, %d, %d) = %q, want %q",
					tt.number, tt.owed, tt.attempt, got, tt.want)
			}
			if again := payment.SessionKey(tt.number, tt.owed, tt.attempt); again != got {
				t.Errorf("SessionKey is not stable: %q then %q — an idempotency key "+
					"that changes per call idempotes nothing", got, again)
			}
		})
	}
}

// TestASessionIsNeverOpenedOnALapsedHold is the rule that binds a Checkout
// Session to the stock behind it.
//
// The session used to be time.Now() + 30 minutes and the hold PlaceOrder + 30
// minutes, with a comment claiming the session was "deliberately shorter". Two
// equal durations measured from different instants are not the same window: the
// session was strictly LONGER by however long the customer sat on the pay page,
// so money could arrive for stock the sweeper had already released and sold.
//
// Stripe will not accept an expires_at less than thirty minutes out, so a hold
// with less than that left has no honest session at all — padding it back up to
// Stripe's floor is exactly the defect. An order holding nothing fails for the
// same reason, and the zero time is what "the sweeper has already taken it back"
// looks like in Go.
func TestASessionIsNeverOpenedOnALapsedHold(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		hold  time.Time
		start bool
	}{
		{"a hold just placed", now.Add(30 * time.Minute), true},
		{"an hour of hold left", now.Add(time.Hour), true},
		// 30 minutes is Stripe's floor, so one second under it is refused.
		{"a second under Stripe's floor", now.Add(30*time.Minute - time.Second), false},
		{"five minutes left", now.Add(5 * time.Minute), false},
		{"the hold ran out", now.Add(-time.Minute), false},
		{"no hold at all", time.Time{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := payment.Order{Number: "GO-1", TotalCents: 199900, HoldExpiresAt: tt.hold}
			if got := o.HoldCoversASession(now); got != tt.start {
				t.Errorf("HoldCoversASession with the hold at %v = %v, want %v",
					tt.hold, got, tt.start)
			}
			if got := o.SessionExpiry(); !got.Equal(tt.hold) {
				t.Errorf("SessionExpiry() = %v, want the hold's own deadline %v — a session "+
					"that expires on any other schedule outlives the stock", got, tt.hold)
			}
		})
	}
}

// TestOtherEventTypesAreNotCaptures proves no other event posts money. goen
// records more than it acts on, and an
// event of another type must never post money.
//
// `checkout.session.async_payment_succeeded` deliberately does NOT belong in
// this list. Adding it here is the instinct when a new event type turns up, and
// it would assert the defect: that event IS a capture, and it is covered by
// TestOnlyAPaidSessionIsACapture expecting true.
func TestOtherEventTypesAreNotCaptures(t *testing.T) {
	g := enabledGateway(t)
	for _, typ := range []string{
		"checkout.session.expired",
		"checkout.session.async_payment_failed",
		"payment_intent.succeeded",
		"payment_intent.payment_failed",
		"charge.refunded",
	} {
		t.Run(typ, func(t *testing.T) {
			ev := sessionEvent("evt_"+typ, "cs_x", "paid", 199900)
			ev["type"] = typ
			body, header := signed(t, ev)
			verified, err := g.VerifyWebhook(body, header)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			if _, ok := payment.CaptureFrom(&verified); ok {
				t.Errorf("%s was treated as a capture", typ)
			}
		})
	}
}

// TestGatewayRefusesAKeyWithoutAWebhookSecret proves goen will not start able to
// take money over an unauthenticated endpoint. The combination is the dangerous
// one: goen would take money over an endpoint nothing authenticates, and the
// failure is silent until somebody posts a made-up capture.
func TestGatewayRefusesAKeyWithoutAWebhookSecret(t *testing.T) {
	_, err := payment.NewGateway("sk_test_something", "", "https://goen.example")
	if err == nil {
		t.Fatal("a Stripe key with no webhook secret was accepted")
	}
	if !strings.Contains(err.Error(), "webhook secret") {
		t.Errorf("error %q does not say what is missing", err)
	}
}

// TestNoKeyIsNotAnError proves a deployment without Stripe still serves the site.
func TestNoKeyIsNotAnError(t *testing.T) {
	g, err := payment.NewGateway("", "", "http://127.0.0.1:9700")
	if err != nil {
		t.Fatalf("goen refused to start without Stripe: %v", err)
	}
	if g.Enabled() {
		t.Error("gateway reports enabled with no key")
	}
	if _, _, err := g.StartSession(t.Context(), &payment.Order{Number: "GO-1"}, 0); !errors.Is(err, payment.ErrDisabled) {
		t.Errorf("StartSession returned %v, want ErrDisabled", err)
	}
	if _, err := g.VerifyWebhook([]byte("{}"), ""); !errors.Is(err, payment.ErrDisabled) {
		t.Errorf("VerifyWebhook returned %v, want ErrDisabled", err)
	}
}

// TestTheLoyaltyConstantsMatchTheProgramme proves the capture awards on the
// terms internal/loyalty publishes.
//
// internal/payment carries its own copies rather than importing internal/loyalty
// for two numbers, and the doc comments on both claimed a test kept them equal.
// No such test existed — a comment promising a guarantee is not a guarantee,
// which is the same shape as outbox.Stuck() having no caller and two comments
// saying it surfaced problems.
func TestTheLoyaltyConstantsMatchTheProgramme(t *testing.T) {
	if got, want := payment.LoyaltyValidityDays, loyalty.Days(loyalty.Validity); got != want {
		t.Errorf("the capture expires points after %d days and the programme says %d",
			got, want)
	}
	if got, want := payment.MembershipWindowDays, loyalty.Days(loyalty.MembershipWindow); got != want {
		t.Errorf("the capture reads a %d-day spend window and the programme says %d",
			got, want)
	}
}
