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
// checkout can still open a Checkout Session when the customer presses Pay.
// Time has to PASS here and the hold has to come from cart.HoldTTL.
func TestAPlacedOrderCanActuallyBePaidFor(t *testing.T) {
	t.Parallel()

	placedAt := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	o := payment.Order{Number: "GO-1", TotalCents: 199900, HoldExpiresAt: placedAt.Add(cart.HoldTTL)}

	tests := []struct {
		name    string
		elapsed time.Duration
		want    bool
	}{
		{name: "one second after placing", elapsed: time.Second, want: true},
		{name: "halfway through the pay window", elapsed: cart.PayWindow / 2, want: true},
		{name: "at the last moment of the pay window", elapsed: cart.PayWindow, want: true},
		// Past the window the remaining hold is under Stripe's floor.
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
// the constant payment owns; a drift is invisible until no session opens.
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
// refused, which is the whole security boundary of the endpoint taking money.
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

// TestWebhookAcceptsAGenuineSignature is the other half: without it the test
// above passes with verification hard-wired to fail.
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

// typed relabels an event.
func typed(ev map[string]any, eventType string) map[string]any {
	ev["type"] = eventType
	return ev
}

func sessionField(ev map[string]any, name string, value any) map[string]any {
	data := ev["data"].(map[string]any)
	object := data["object"].(map[string]any)
	object[name] = value
	return ev
}

// TestOnlyAPaidSessionIsACapture proves an unpaid session is not treated as
// money received, and that the paid one of EACH event type is.
func TestOnlyAPaidSessionIsACapture(t *testing.T) {
	g := enabledGateway(t)

	tests := []struct {
		name         string
		event        map[string]any
		isCapture    bool
		isUnreadable bool
	}{
		{name: "paid", event: sessionEvent("e1", "cs_1", "paid", 199900), isCapture: true},
		{name: "unpaid", event: sessionEvent("e2", "cs_2", "unpaid", 199900)},
		{
			name: "still processing", event: sessionEvent("e3", "cs_3", "no_payment_required", 199900),
			isUnreadable: true,
		},
		{name: "zero amount", event: sessionEvent("e4", "cs_4", "paid", 0), isUnreadable: true},
		{
			name:      "asynchronous method that cleared",
			event:     typed(sessionEvent("e5", "cs_5", "paid", 199900), "checkout.session.async_payment_succeeded"),
			isCapture: true,
		},
		{
			name:         "asynchronous method still unpaid",
			event:        typed(sessionEvent("e6", "cs_6", "unpaid", 199900), "checkout.session.async_payment_succeeded"),
			isUnreadable: true,
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
			_, isAbandoned := payment.AbandonedSessionFrom(&ev)
			_, isUnsettled := payment.UnsettledSessionFrom(&ev)
			// Every event in this table has a checkout type. The package-internal
			// classifier test owns that closed type set; this test owns whether
			// the payload readers can understand each shape.
			unreadable := !got && !isAbandoned && !isUnsettled
			if unreadable != tt.isUnreadable {
				t.Errorf("unreadable = %v, want %v", unreadable, tt.isUnreadable)
			}
		})
	}
}

// TestAKnownEventGoenCannotReadIsNotAnEventItIgnores holds the three webhook
// states apart: an unhandled type is ordinary, a known and understood event has
// a branch, and a known event whose object no reader can decode needs a person.
func TestAKnownEventGoenCannotReadIsNotAnEventItIgnores(t *testing.T) {
	g := enabledGateway(t)

	// ConstructEvent rejects a genuinely truncated signed JSON document before
	// classification. These syntactically valid events are the reachable
	// equivalents: one has a type-incompatible field in data.object and one has
	// a null data.object that no Checkout Session reader can understand.
	typeIncompatibleObject := sessionField(
		sessionEvent("evt_bad_object", "cs_bad_object", "paid", 67000),
		"id", map[string]any{"remaining": "cs_bad_object"})
	nullObject := sessionEvent("evt_null_object", "cs_null_object", "paid", 67000)
	nullObject["data"].(map[string]any)["object"] = nil
	omittedData := sessionEvent("evt_omitted_data", "cs_omitted_data", "paid", 67000)
	delete(omittedData, "data")
	nullData := sessionEvent("evt_null_data", "cs_null_data", "paid", 67000)
	nullData["data"] = nil

	tests := []struct {
		name           string
		event          map[string]any
		wantActionable bool
		wantUnderstood bool
		wantUnreadable bool
	}{
		{
			name: "amount has the wrong type",
			event: sessionField(sessionEvent("evt_bad_amount", "cs_bad_amount", "paid", 67000),
				"amount_total", "67000"),
			wantActionable: true, wantUnreadable: true,
		},
		{
			name: "payment status has the wrong type",
			event: sessionField(sessionEvent("evt_bad_status", "cs_bad_status", "paid", 67000),
				"payment_status", map[string]any{"v": "paid"}),
			wantActionable: true, wantUnreadable: true,
		},
		{
			name: "known event with a type-incompatible object", event: typeIncompatibleObject,
			wantActionable: true, wantUnreadable: true,
		},
		{
			name: "known event with a null object", event: nullObject,
			wantActionable: true, wantUnreadable: true,
		},
		{
			name: "known event with omitted data", event: omittedData,
			wantActionable: true, wantUnreadable: true,
		},
		{
			name: "known event with null data", event: nullData,
			wantActionable: true, wantUnreadable: true,
		},
		{
			name:           "completed with an unknown payment status",
			event:          sessionEvent("evt_new_status", "cs_new_status", "no_payment_required", 67000),
			wantActionable: true, wantUnreadable: true,
		},
		{
			name: "async success that is not paid",
			event: typed(sessionEvent("evt_async_unpaid", "cs_async_unpaid", "unpaid", 67000),
				"checkout.session.async_payment_succeeded"),
			wantActionable: true, wantUnreadable: true,
		},
		{
			name: "expired event with an unreadable session id",
			event: typed(sessionField(sessionEvent("evt_expired_bad", "cs_expired_bad", "unpaid", 67000),
				"id", []string{"cs_expired_bad"}), "checkout.session.expired"),
			wantActionable: true, wantUnreadable: true,
		},
		{
			name: "paid completed", event: sessionEvent("evt_paid", "cs_paid", "paid", 67000),
			wantActionable: true, wantUnderstood: true,
		},
		{
			name: "paid async success",
			event: typed(sessionEvent("evt_async_paid", "cs_async_paid", "paid", 67000),
				"checkout.session.async_payment_succeeded"),
			wantActionable: true, wantUnderstood: true,
		},
		{
			name: "good expired",
			event: typed(sessionEvent("evt_expired", "cs_expired", "unpaid", 67000),
				"checkout.session.expired"),
			wantActionable: true, wantUnderstood: true,
		},
		{
			name: "good async failure",
			event: typed(sessionEvent("evt_async_failed", "cs_async_failed", "unpaid", 67000),
				"checkout.session.async_payment_failed"),
			wantActionable: true, wantUnderstood: true,
		},
		{
			name:           "completed but unpaid is understood",
			event:          sessionEvent("evt_unpaid", "cs_unpaid", "unpaid", 67000),
			wantActionable: true, wantUnderstood: true,
		},
		{
			name:  "unrelated event is ignored",
			event: typed(sessionEvent("evt_customer", "cus_customer", "unpaid", 67000), "customer.created"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, header := signed(t, tt.event)
			ev, err := g.VerifyWebhook(body, header)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			_, isCapture := payment.CaptureFrom(&ev)
			_, isAbandoned := payment.AbandonedSessionFrom(&ev)
			_, isUnsettled := payment.UnsettledSessionFrom(&ev)
			understood := isCapture || isAbandoned || isUnsettled
			unreadable := tt.wantActionable && !understood

			if understood != tt.wantUnderstood {
				t.Errorf("understood = %v, want %v", understood, tt.wantUnderstood)
			}
			if unreadable != tt.wantUnreadable {
				t.Errorf("unreadable = %v, want %v", unreadable, tt.wantUnreadable)
			}
		})
	}
}

// TestASessionThatEndsWithNoMoneyIsAbandoned holds the other half of the
// asynchronous pair. The two readers must not overlap: one cancels a payment
// and the other posts money.
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
// the two readers above deliberately drop: a delayed method's completed session
// is the only warning goen gets that its stock model has stopped holding.
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
			name:  "paid on the spot",
			event: sessionEvent("evt_u2", "cs_u2", "paid", 199900),
		},
		{
			name:  "nothing to pay",
			event: sessionEvent("evt_u3", "cs_u3", "no_payment_required", 199900),
		},
		{
			name:  "the delayed money cleared later",
			event: typed(sessionEvent("evt_u4", "cs_u4", "paid", 199900), "checkout.session.async_payment_succeeded"),
		},
		{
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
				// Money is in flight: neither posting nor cancelling is legal.
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
// defence against one order being charged twice: the same three inputs must
// produce the same string, and the string must move when any of them does.
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
// Session to the stock behind it: a hold with less than Stripe's floor left has
// no honest session at all, and padding it back up to that floor is the defect.
func TestASessionIsNeverOpenedOnALapsedHold(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		hold  time.Time
		start bool
	}{
		// `now` is both the base and the instant the gate is asked at, so this
		// is the zero-elapsed case and nothing else.
		{"exactly at Stripe's floor", now.Add(30 * time.Minute), true},
		{"an hour of hold left", now.Add(time.Hour), true},
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

// TestOtherEventTypesAreNotCaptures proves no other event posts money.
// `checkout.session.async_payment_succeeded` does NOT belong here: it IS one.
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
// take money over an endpoint nothing authenticates.
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
// terms internal/loyalty publishes, which payment copies rather than imports.
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
