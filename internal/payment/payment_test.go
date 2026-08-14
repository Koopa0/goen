package payment_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	stripe "github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/loyalty"
	"github.com/koopa0/goen/internal/payment"
)

// TestAPlacedOrderCanActuallyBePaidFor holds that an order placed through
// checkout can still open a Checkout Session when the customer presses Pay. Its
// absence is goen unable to take a single payment.
//
// cart.HoldTTL and payment.MinSessionLifetime are measured from DIFFERENT
// instants: the hold is stamped at PlaceOrder, the gate is asked when the
// customer presses Pay. Equal, `HoldExpiresAt >= pay_at + MinSessionLifetime`
// reduces to `placed_at >= pay_at` — false one second after checkout, so every
// Stripe-configured deployment answers 409 on /orders/{number}/pay, forever.
//
// Two neighbouring tests are false-green on that BY CONSTRUCTION, and the shapes
// are worth naming:
//
//   - TestASessionIsNeverOpenedOnALapsedHold below passes ONE frozen `now` as
//     both the base the hold is computed from and the instant the gate is asked
//     at, so elapsed time is zero — the single point where the equality holds.
//   - The integration suite writes 35- and 45-minute holds through a fixture
//     helper rather than through PlaceOrder, so it never uses HoldTTL at all;
//     its own failure message ("35 minutes of hold is not enough to open a
//     30-minute session") shows the numbers are picked to clear the floor.
//
// This one therefore does the thing neither does: it lets time PASS between
// placing and paying, and it derives the hold from cart.HoldTTL rather than
// from a literal. A constant that drifts into the equality turns it red.
func TestAPlacedOrderCanActuallyBePaidFor(t *testing.T) {
	t.Parallel()

	placedAt := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	// What PlaceOrder writes: internal/cart/store.go — time.Now().Add(HoldTTL).
	o := payment.Order{Number: "GO-1", TotalCents: 199900, HoldExpiresAt: placedAt.Add(cart.HoldTTL)}

	tests := []struct {
		name    string
		elapsed time.Duration
		want    bool
	}{
		{name: "one second after placing", elapsed: time.Second, want: true},
		{name: "halfway through the pay window", elapsed: cart.PayWindow / 2, want: true},
		{name: "at the last moment of the pay window", elapsed: cart.PayWindow, want: true},
		// Past the window the remaining hold is under Stripe's floor, and a
		// session padded out to that floor would outlive the goods, which is
		// exactly what binding the expiry to the reservation exists to stop.
		{name: "one second past the pay window", elapsed: cart.PayWindow + time.Second, want: false},
		{name: "long past it", elapsed: cart.HoldTTL, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := o.HoldCoversASession(placedAt.Add(tt.elapsed)); got != tt.want {
				t.Errorf("HoldCoversASession(%s after placing) = %v, want %v — "+
					"cart.HoldTTL=%s, cart.PayWindow=%s, payment.MinSessionLifetime=%s",
					tt.elapsed, got, tt.want,
					cart.HoldTTL, cart.PayWindow, payment.MinSessionLifetime)
			}
		})
	}
}

// TestTheMirroredStripeFloorMatchesTheRealOne binds the copy cart keeps against
// the constant payment owns.
//
// cart derives HoldTTL from its mirror of Stripe's session floor, and internal/
// payment holds the real one. Nothing else points the two at each other, so a
// drift between them is invisible until no session can be opened at all.
func TestTheMirroredStripeFloorMatchesTheRealOne(t *testing.T) {
	t.Parallel()

	if cart.StripeSessionFloor != payment.MinSessionLifetime {
		t.Errorf("cart.StripeSessionFloor = %s, payment.MinSessionLifetime = %s — "+
			"cart sizes its stock hold from its copy, so a drift here silently "+
			"changes whether a session can be opened at all",
			cart.StripeSessionFloor, payment.MinSessionLifetime)
	}
	if cart.HoldTTL <= payment.MinSessionLifetime {
		t.Errorf("cart.HoldTTL = %s is not longer than payment.MinSessionLifetime = %s, "+
			"so no session can ever be created: the hold is stamped at placement and "+
			"read at pay time, so the gate reduces to placed_at >= pay_at",
			cart.HoldTTL, payment.MinSessionLifetime)
	}
}

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
// The asynchronous rows are defence in depth rather than the ordinary path: the
// session pins card, because a stock hold cannot outlive a delayed method. A
// delayed method's `completed` arrives with payment_status `unpaid` and is
// refused by the row above it; the success that FOLLOWS is
// `checkout.session.async_payment_succeeded` and nothing else, so a reader that
// does not accept it leaves the customer paid and the order unpaid forever. It
// belongs here expecting true, and NOT in TestOtherEventTypesAreNotCaptures,
// which is where the instinct to "add the new event type" would put it and lock
// the bug in.
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
// `checkout.session.expired` does, so it goes to the same place. Routed nowhere,
// the row sits at requires_payment for ever and reconciliation cannot tell a
// dead checkout from one still in flight.
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

// TestACompletedButUnpaidSessionIsReportedRatherThanIgnored holds the one signal
// the two readers above deliberately drop.
//
// A delayed payment method's `checkout.session.completed` arrives with
// payment_status `unpaid`. CaptureFrom refuses it, correctly — no money has
// moved. AbandonedSessionFrom refuses it, correctly — nothing failed, and a
// COMPLETED session never fires `checkout.session.expired`. With no reader of
// its own it falls through to the webhook handler's default branch and is logged
// as one more event goen does not act on, indistinguishable from the dozen it
// genuinely does not.
//
// It is the ONLY warning goen gets that its stock model has stopped holding. The
// session is bounded by the stock hold on purpose; a delayed method settles days
// after it, so the sweeper returns the units to the shelf, they are re-sold, and
// the capture then succeeds against stock that is gone — capture_payment does
// not read reservations and admin.Ship ranges over an empty slice without error.
//
// The three readers must not overlap, and the last two cases are what prove it:
// one of them posts money and one cancels a payment, so a session with money
// genuinely in flight must be claimed by neither.
func TestACompletedButUnpaidSessionIsReportedRatherThanIgnored(t *testing.T) {
	g := enabledGateway(t)

	tests := []struct {
		name        string
		event       map[string]any
		isUnsettled bool
	}{
		{
			name:        "a delayed method completed the checkout",
			event:       sessionEvent("evt_u1", "cs_u1", "unpaid", 199900),
			isUnsettled: true,
		},
		{
			// The ordinary card checkout, and the case that must NOT alarm.
			name:  "paid on the spot",
			event: sessionEvent("evt_u2", "cs_u2", "paid", 199900),
		},
		{
			// Never reaches Stripe: a zero-owed order is not sent at all, because
			// Stripe refuses a zero-amount session.
			name:  "nothing to pay",
			event: sessionEvent("evt_u3", "cs_u3", "no_payment_required", 199900),
		},
		{
			// The money ARRIVING is a capture, not an alarm.
			name:  "the delayed money cleared later",
			event: typed(sessionEvent("evt_u4", "cs_u4", "paid", 199900), "checkout.session.async_payment_succeeded"),
		},
		{
			// Already handled: this one cancels the payment row.
			name:  "the delayed money never cleared",
			event: typed(sessionEvent("evt_u5", "cs_u5", "unpaid", 199900), "checkout.session.async_payment_failed"),
		},
		{
			name:  "an abandoned checkout expiring",
			event: typed(sessionEvent("evt_u6", "cs_u6", "unpaid", 199900), "checkout.session.expired"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, header := signed(t, tt.event)
			ev, err := g.VerifyWebhook(body, header)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}

			session, got := payment.UnsettledSessionFrom(&ev)
			if got != tt.isUnsettled {
				t.Errorf("UnsettledSessionFrom() says %v, want %v", got, tt.isUnsettled)
			}
			if tt.isUnsettled {
				if session != "cs_u1" {
					t.Errorf("UnsettledSessionFrom() named session %q, want cs_u1", session)
				}
				// The non-overlap that matters: money is in flight, so neither
				// posting it nor cancelling the row is a legal reading of this
				// event.
				if _, isCapture := payment.CaptureFrom(&ev); isCapture {
					t.Error("CaptureFrom() claims a session whose money has not arrived — " +
						"capturing here marks an order paid days before the funds clear")
				}
				if _, isAbandoned := payment.AbandonedSessionFrom(&ev); isAbandoned {
					t.Error("AbandonedSessionFrom() claims a session still in flight — " +
						"cancelling here throws away the record that money is coming")
				}
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
// A session of time.Now() + 30 minutes against a hold of PlaceOrder + 30 minutes
// looks deliberately equal and is not: two equal durations measured from
// different instants are not the same window. The session is strictly LONGER by
// however long the customer sat on the pay page, so money arrives for stock the
// sweeper has already released and sold.
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
		// NOT "a hold just placed": `now` is both the base and the instant the
		// gate is asked at here, so this is the zero-elapsed case and nothing
		// else — exactly at Stripe's floor. Naming it for a freshly placed order
		// would make the whole table read as covering the real flow, which it
		// does not; TestAPlacedOrderCanActuallyBePaidFor is the one that lets
		// time pass.
		{"exactly at Stripe's floor", now.Add(30 * time.Minute), true},
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
