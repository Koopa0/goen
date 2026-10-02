//go:build integration

package payment_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	stripe "github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/payment"
)

// moneyState is everything a webhook could have changed about one order's
// money: the payment row, whether the order counts as paid, and the side
// effects a capture appends.
type moneyState struct {
	status     string
	captured   *int64
	committed  bool
	paidEvents int
	receipts   int
}

func moneyOf(t *testing.T, ctx context.Context, number string, orderID uuid.UUID, session string) moneyState {
	t.Helper()
	var m moneyState
	if err := pool.QueryRow(ctx,
		`SELECT status, captured_amount_cents FROM payments WHERE provider_ref = $1`,
		session).Scan(&m.status, &m.captured); err != nil {
		t.Fatalf("read payment %s: %v", session, err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT order_is_committed($1),
		       (SELECT count(*) FROM order_events WHERE order_id = $1 AND kind = 'paid'),
		       (SELECT count(*) FROM outbox_messages WHERE topic = 'order.paid' AND dedupe_key = $2)`,
		orderID, number).Scan(&m.committed, &m.paidEvents, &m.receipts); err != nil {
		t.Fatalf("read money state of order %s: %v", number, err)
	}
	return m
}

func (m moneyState) untouched() bool {
	return m.status == "requires_payment" && m.captured == nil && !m.committed &&
		m.paidEvents == 0 && m.receipts == 0
}

func deliver(t *testing.T, h *payment.Handler, body []byte, header string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/webhooks/stripe", bytes.NewReader(body))
	if header != "" {
		req.Header.Set("Stripe-Signature", header)
	}
	w := httptest.NewRecorder()
	h.Webhook(w, req)
	return w
}

func eventRows(t *testing.T, ctx context.Context, eventID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payment_webhook_events WHERE event_id = $1`, eventID).Scan(&n); err != nil {
		t.Fatalf("count webhook events: %v", err)
	}
	return n
}

// openOrder is a placed order with an open Checkout Session for all it owes.
func openOrder(t *testing.T, s *payment.Store, cents int64, label string) (number string, id uuid.UUID, session string) {
	t.Helper()
	number, id = order(t, cents)
	session = "cs_" + label + "_" + uuid.NewString()[:12]
	if err := s.OpenPayment(t.Context(), number, session, cents); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	return number, id, session
}

// TestACaptureInAnotherCurrencyNeverMarksTheOrderPaid holds that the amount a
// session reports is money only in the currency the order is priced in. The
// same number of another currency's minor units is verified money that goen
// did not ask for: it must stay an operator's alarm, never a paid order.
func TestACaptureInAnotherCurrencyNeverMarksTheOrderPaid(t *testing.T) {
	tests := []struct {
		name     string
		currency any // nil drops the field from the session
		wantPaid bool
	}{
		{name: "the currency the order is priced in", currency: "twd", wantPaid: true},
		{name: "another currency for the same minor units", currency: "usd"},
		{name: "a session that names no currency", currency: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			s := payment.NewStore(pool)
			var logs bytes.Buffer
			h := payment.NewHandler(s, enabledGateway(t), alwaysPlacedHere{},
				slog.New(slog.NewTextHandler(&logs, nil)), false)
			number, id, session := openOrder(t, s, 100000, "currency")

			eventID := "evt_currency_" + uuid.NewString()[:12]
			ev := sessionEvent(eventID, session, "paid", 100000)
			object := ev["data"].(map[string]any)["object"].(map[string]any)
			if tt.currency == nil {
				delete(object, "currency")
			} else {
				object["currency"] = tt.currency
			}
			body, header := signed(t, ev)
			if w := deliver(t, h, body, header); w.Code != http.StatusOK {
				t.Fatalf("Webhook() status = %d, want 200 — a retry carries the same currency", w.Code)
			}

			got := moneyOf(t, ctx, number, id, session)
			if tt.wantPaid {
				if got.status != "succeeded" || !got.committed || got.paidEvents != 1 || got.receipts != 1 {
					t.Fatalf("a capture in the order's own currency left %+v, want it paid once", got)
				}
				return
			}
			if !got.untouched() {
				t.Errorf("a capture in currency %v left %+v, want the order unpaid and nothing appended",
					tt.currency, got)
			}
			var reason *string
			if err := pool.QueryRow(ctx,
				`SELECT unreconciled FROM payment_webhook_events WHERE event_id = $1`,
				eventID).Scan(&reason); err != nil {
				t.Fatalf("read webhook outcome: %v", err)
			}
			if reason == nil || !strings.HasPrefix(*reason, "refused_capture: ") ||
				!strings.Contains(*reason, "not "+payment.Currency) {
				t.Errorf("unreconciled = %v, want a refused_capture naming the currency", reason)
			}
			attempt, err := s.PaymentAttempt(ctx, number, 100000)
			if err != nil {
				t.Fatalf("PaymentAttempt: %v", err)
			}
			if !attempt.NeedsReconciliation {
				t.Error("money taken in another currency did not block a second checkout")
			}
			if !strings.Contains(logs.String(), "level=ERROR") {
				t.Errorf("refused capture log = %q, want an ERROR somebody acts on", logs.String())
			}
		})
	}
}

// TestAWebhookThatDoesNotVerifyChangesNothing holds the endpoint's whole
// boundary: bytes Stripe did not sign within the tolerance are refused before
// anything is recorded, so the claim that would make a genuine retry look
// already processed is never taken either.
func TestAWebhookThatDoesNotVerifyChangesNothing(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	h := payment.NewHandler(s, enabledGateway(t), alwaysPlacedHere{},
		slog.New(slog.DiscardHandler), false)

	const owed = 88000
	signedAt := func(body []byte, at time.Time) string {
		return stripe.GenerateTestSignedPayload(&stripe.UnsignedPayload{
			Payload: body, Secret: testWebhookSecret, Timestamp: at,
		}).Header
	}
	marshal := func(ev map[string]any) []byte {
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return raw
	}

	// Each case turns the genuine paid event for a session into what is sent.
	tests := []struct {
		name string
		send func(session, eventID string, genuine []byte) (body []byte, header string)
		want int
	}{
		{
			name: "no signature",
			send: func(_, _ string, genuine []byte) ([]byte, string) {
				return genuine, ""
			},
			want: http.StatusBadRequest,
		},
		{
			name: "signed with another secret",
			send: func(_, _ string, genuine []byte) ([]byte, string) {
				return genuine, otherSecretHeader(t, genuine)
			},
			want: http.StatusBadRequest,
		},
		{
			name: "the body changed after it was signed",
			send: func(session, eventID string, genuine []byte) ([]byte, string) {
				return marshal(sessionEvent(eventID, session, "paid", 1)), signedAt(genuine, time.Now())
			},
			want: http.StatusBadRequest,
		},
		{
			name: "a genuine signature outside the tolerance",
			send: func(_, _ string, genuine []byte) ([]byte, string) {
				return genuine, signedAt(genuine, time.Now().Add(-10*time.Minute))
			},
			want: http.StatusBadRequest,
		},
		{
			name: "a timestamp with no signature",
			send: func(_, _ string, genuine []byte) ([]byte, string) {
				return genuine, "t=" + strconv.FormatInt(time.Now().Unix(), 10)
			},
			want: http.StatusBadRequest,
		},
		{
			name: "a signed body past the size the endpoint reads",
			send: func(_, _ string, genuine []byte) ([]byte, string) {
				big := append(bytes.TrimSuffix(genuine, []byte("}")),
					[]byte(`,"pad":"`+strings.Repeat("x", 1<<20)+`"}`)...)
				return big, signedAt(big, time.Now())
			},
			want: http.StatusRequestEntityTooLarge,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			number, id, session := openOrder(t, s, owed, "unverified")
			eventID := "evt_unverified_" + uuid.NewString()[:12]
			genuine := marshal(sessionEvent(eventID, session, "paid", owed))

			body, header := tt.send(session, eventID, genuine)
			if w := deliver(t, h, body, header); w.Code != tt.want {
				t.Fatalf("Webhook() status = %d, want %d", w.Code, tt.want)
			}
			if n := eventRows(t, ctx, eventID); n != 0 {
				t.Errorf("an unverified delivery recorded %d webhook event rows, want 0", n)
			}
			if got := moneyOf(t, ctx, number, id, session); !got.untouched() {
				t.Fatalf("an unverified delivery left %+v, want the order unpaid", got)
			}

			// Stripe's own delivery of the same event still lands: the refusal
			// was about the bytes, and it took no claim on the event id.
			if w := deliver(t, h, genuine, signedAt(genuine, time.Now())); w.Code != http.StatusOK {
				t.Fatalf("genuine Webhook() status = %d, want 200", w.Code)
			}
			if got := moneyOf(t, ctx, number, id, session); got.status != "succeeded" || !got.committed {
				t.Errorf("the genuine delivery after a refusal left %+v, want the order paid", got)
			}
		})
	}
}

// TestARedeliveredEventChangesStateOnce holds at-least-once delivery to one
// effect: the same signed bytes delivered again are acknowledged and change
// nothing, and a second event about the same paid session posts nothing more.
func TestARedeliveredEventChangesStateOnce(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	h := payment.NewHandler(s, enabledGateway(t), alwaysPlacedHere{},
		slog.New(slog.DiscardHandler), false)
	number, id, session := openOrder(t, s, 64000, "redelivered")

	eventID := "evt_redelivered_" + uuid.NewString()[:12]
	body, header := signed(t, sessionEvent(eventID, session, "paid", 64000))
	if w := deliver(t, h, body, header); w.Code != http.StatusOK {
		t.Fatalf("first delivery status = %d, want 200", w.Code)
	}
	first := moneyOf(t, ctx, number, id, session)
	if first.status != "succeeded" || first.paidEvents != 1 || first.receipts != 1 {
		t.Fatalf("first delivery left %+v, want one capture", first)
	}
	processedAt := func() time.Time {
		t.Helper()
		var at time.Time
		if err := pool.QueryRow(ctx,
			`SELECT processed_at FROM payment_webhook_events WHERE event_id = $1`,
			eventID).Scan(&at); err != nil {
			t.Fatalf("read processed_at: %v", err)
		}
		return at
	}
	claimedAt := processedAt()

	for i := range 2 {
		if w := deliver(t, h, body, header); w.Code != http.StatusOK {
			t.Fatalf("redelivery %d status = %d, want 200 so Stripe stops sending it", i, w.Code)
		}
	}
	if again := processedAt(); !again.Equal(claimedAt) {
		t.Errorf("a redelivery re-applied the event: processed_at moved from %v to %v", claimedAt, again)
	}
	if n := eventRows(t, ctx, eventID); n != 1 {
		t.Errorf("%d rows for one event id, want 1", n)
	}

	secondID := "evt_redelivered_second_" + uuid.NewString()[:12]
	secondBody, secondHeader := signed(t, typed(sessionEvent(secondID, session, "paid", 64000),
		"checkout.session.async_payment_succeeded"))
	if w := deliver(t, h, secondBody, secondHeader); w.Code != http.StatusOK {
		t.Fatalf("second event status = %d, want 200", w.Code)
	}
	if got := moneyOf(t, ctx, number, id, session); got.paidEvents != 1 || got.receipts != 1 ||
		got.captured == nil || *got.captured != 64000 {
		t.Errorf("after redeliveries and a second event the order reads %+v, want one capture of 64000", got)
	}
}

// TestOnlyTheEventsGoenActsOnMoveMoney holds the event-type allowlist at the
// door. A signed event of any other type is recorded and acknowledged, and
// never marks the order paid, however much its object looks like a paid
// Checkout Session for a payment goen opened.
func TestOnlyTheEventsGoenActsOnMoveMoney(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	h := payment.NewHandler(s, enabledGateway(t), alwaysPlacedHere{},
		slog.New(slog.DiscardHandler), false)

	for _, eventType := range []string{
		"payment_intent.succeeded",
		"charge.succeeded",
		"charge.captured",
		"invoice.paid",
		"checkout.session.updated",
	} {
		t.Run(eventType, func(t *testing.T) {
			number, id, session := openOrder(t, s, 51000, "ignored")
			eventID := "evt_ignored_" + uuid.NewString()[:12]
			body, header := signed(t, typed(sessionEvent(eventID, session, "paid", 51000), eventType))
			if w := deliver(t, h, body, header); w.Code != http.StatusOK {
				t.Fatalf("Webhook() status = %d, want 200 — an ignored type must not be retried", w.Code)
			}
			if got := moneyOf(t, ctx, number, id, session); !got.untouched() {
				t.Errorf("%s left %+v, want the order unpaid", eventType, got)
			}
			var reason *string
			if err := pool.QueryRow(ctx,
				`SELECT unreconciled FROM payment_webhook_events WHERE event_id = $1`,
				eventID).Scan(&reason); err != nil {
				t.Fatalf("read the recorded event: %v", err)
			}
			if reason != nil {
				t.Errorf("an ignored %s was flagged %q", eventType, *reason)
			}
		})
	}
}

// TestAnEventForASessionGoenNeverOpenedTouchesNoPayment holds attribution to
// goen's own payment rows. An event naming a session goen never created
// invents no payment and closes none of the payments goen did open.
func TestAnEventForASessionGoenNeverOpenedTouchesNoPayment(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	h := payment.NewHandler(s, enabledGateway(t), alwaysPlacedHere{},
		slog.New(slog.DiscardHandler), false)
	number, id, session := openOrder(t, s, 47000, "bystander")

	for _, tt := range []struct {
		eventType, payStatus, wantReason string
	}{
		{eventType: "checkout.session.expired", payStatus: "unpaid"},
		{eventType: "checkout.session.async_payment_failed", payStatus: "unpaid"},
		{eventType: "checkout.session.completed", payStatus: "paid", wantReason: "unattributed_capture: "},
	} {
		t.Run(tt.eventType, func(t *testing.T) {
			stranger := "cs_never_opened_" + uuid.NewString()[:12]
			eventID := "evt_stranger_" + uuid.NewString()[:12]
			body, header := signed(t, typed(sessionEvent(eventID, stranger, tt.payStatus, 47000), tt.eventType))
			if w := deliver(t, h, body, header); w.Code != http.StatusOK {
				t.Fatalf("Webhook() status = %d, want 200 — a retry cannot make the session goen's", w.Code)
			}
			var invented int
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM payments WHERE provider_ref = $1`, stranger).Scan(&invented); err != nil {
				t.Fatalf("count payments: %v", err)
			}
			if invented != 0 {
				t.Errorf("the webhook invented %d payment rows for a session goen never opened", invented)
			}
			if got := moneyOf(t, ctx, number, id, session); !got.untouched() {
				t.Errorf("an event about another session left goen's open payment %+v", got)
			}
			var reason *string
			if err := pool.QueryRow(ctx,
				`SELECT unreconciled FROM payment_webhook_events WHERE event_id = $1`,
				eventID).Scan(&reason); err != nil {
				t.Fatalf("read the recorded event: %v", err)
			}
			switch {
			case tt.wantReason == "" && reason != nil:
				t.Errorf("unreconciled = %q, want none", *reason)
			case tt.wantReason != "" && (reason == nil || !strings.HasPrefix(*reason, tt.wantReason)):
				t.Errorf("unreconciled = %v, want prefix %q", reason, tt.wantReason)
			}
		})
	}
}

// TestTheWebhookLogsNoSecretSignatureOrCustomerData holds what an operator can
// read in the logs of the one endpoint that takes money. Every outcome is
// driven — refused, stale, captured, redelivered, refused capture and
// unattributed — at DEBUG, and no logged value may carry the signing secret, a
// signature, the payload, or the customer and card facts inside it.
func TestTheWebhookLogsNoSecretSignatureOrCustomerData(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	var logs bytes.Buffer
	h := payment.NewHandler(s, enabledGateway(t), alwaysPlacedHere{},
		slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})), false)

	const (
		customerEmail = "log-probe-customer@example.com"
		customerName  = "記錄探針"
		cardLast4     = "9731"
	)
	withCustomer := func(ev map[string]any) map[string]any {
		sessionField(ev, "customer_details", map[string]any{
			"email": customerEmail, "name": customerName,
		})
		return sessionField(ev, "payment_intent", map[string]any{
			"id": "pi_log_probe", "object": "payment_intent",
			"latest_charge": map[string]any{
				"id": "ch_log_probe", "object": "charge",
				"payment_method_details": map[string]any{
					"type": "card",
					"card": map[string]any{"brand": "visa", "last4": cardLast4},
				},
			},
		})
	}

	signedNow := func(raw []byte) string {
		return stripe.GenerateTestSignedPayload(&stripe.UnsignedPayload{
			Payload: raw, Secret: testWebhookSecret,
		}).Header
	}

	paidNumber, paidID, paidSession := openOrder(t, s, 73000, "logprobe")
	_, _, foreignSession := openOrder(t, s, 73000, "logforeign")

	var headers []string
	var payloads [][]byte
	deliverRaw := func(raw []byte, header string) {
		t.Helper()
		headers = append(headers, header)
		payloads = append(payloads, raw)
		deliver(t, h, raw, header)
	}
	marshal := func(ev map[string]any) []byte {
		t.Helper()
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return raw
	}

	forged := marshal(withCustomer(sessionEvent("evt_log_forged_"+uuid.NewString()[:8], paidSession, "paid", 73000)))
	deliverRaw(forged, otherSecretHeader(t, forged))

	stale := marshal(withCustomer(sessionEvent("evt_log_stale_"+uuid.NewString()[:8], paidSession, "paid", 73000)))
	deliverRaw(stale, stripe.GenerateTestSignedPayload(&stripe.UnsignedPayload{
		Payload: stale, Secret: testWebhookSecret, Timestamp: time.Now().Add(-time.Hour),
	}).Header)

	paid := marshal(withCustomer(sessionEvent("evt_log_paid_"+uuid.NewString()[:8], paidSession, "paid", 73000)))
	paidHeader := signedNow(paid)
	deliverRaw(paid, paidHeader)
	deliverRaw(paid, paidHeader)

	foreign := withCustomer(sessionEvent("evt_log_foreign_"+uuid.NewString()[:8], foreignSession, "paid", 73000))
	sessionField(foreign, "currency", "usd")
	foreignRaw := marshal(foreign)
	deliverRaw(foreignRaw, signedNow(foreignRaw))

	stranger := marshal(withCustomer(sessionEvent("evt_log_stranger_"+uuid.NewString()[:8],
		"cs_log_stranger_"+uuid.NewString()[:8], "paid", 73000)))
	deliverRaw(stranger, signedNow(stranger))

	if got := moneyOf(t, ctx, paidNumber, paidID, paidSession); got.status != "succeeded" {
		t.Fatalf("the probe capture did not land (%+v); the log would not cover a capture", got)
	}

	forbidden := []string{testWebhookSecret, "whsec_", customerEmail, customerName, "****" + cardLast4, "•••• " + cardLast4}
	for _, header := range headers {
		forbidden = append(forbidden, header)
		for part := range strings.SplitSeq(header, ",") {
			if sig, ok := strings.CutPrefix(part, "v1="); ok {
				forbidden = append(forbidden, sig)
			}
		}
	}
	// A payload logged as text or as bytes (which slog's JSON handler writes as
	// base64) starts with the same leading bytes either way.
	for _, raw := range payloads {
		forbidden = append(forbidden, string(raw[:48]), base64.StdEncoding.EncodeToString(raw[:48]))
	}

	var lines, warnings int
	sc := bufio.NewScanner(&logs)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		lines++
		var record map[string]any
		if err := json.Unmarshal(sc.Bytes(), &record); err != nil {
			t.Fatalf("log line is not JSON: %v", err)
		}
		if record["level"] == "WARN" || record["level"] == "ERROR" {
			warnings++
		}
		walkLogValues("", record, func(key, text string) {
			if text == cardLast4 {
				t.Errorf("log %q = %q: the card's last four digits reached the log", key, text)
			}
			if strings.Contains(text, `"payment_status"`) || strings.Contains(text, `"customer_details"`) {
				t.Errorf("log %q carries webhook payload JSON: %q", key, text)
			}
			for _, secret := range forbidden {
				if strings.Contains(text, secret) {
					t.Errorf("log %q carries %q", key, secret)
				}
			}
		})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read logs: %v", err)
	}
	if lines == 0 || warnings == 0 {
		t.Fatalf("the probe logged %d lines and %d warnings — it did not exercise the refusals", lines, warnings)
	}
}

// walkLogValues visits every string in a decoded JSON log record, groups
// included, under its dotted key.
func walkLogValues(prefix string, value any, visit func(key, text string)) {
	switch v := value.(type) {
	case string:
		visit(prefix, v)
	case map[string]any:
		for key, nested := range v {
			walkLogValues(strings.TrimPrefix(prefix+"."+key, "."), nested, visit)
		}
	case []any:
		for _, nested := range v {
			walkLogValues(prefix, nested, visit)
		}
	}
}

// TestAPaidCheckoutRecordsTheCardStripeReports holds the card facts on the
// payment row. Stripe's checkout event names the PaymentIntent as a bare id and
// never carries its charge, so the brand and last four only exist after one
// read of the intent with its charge expanded.
func TestAPaidCheckoutRecordsTheCardStripeReports(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	var expanded atomic.Bool
	stripeStandIn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/payment_intents/pi_card_probe" {
			expanded.Store(strings.Contains(r.URL.RawQuery, "latest_charge"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"pi_card_probe","object":"payment_intent","latest_charge":{`+
				`"id":"ch_card_probe","object":"charge","payment_method_details":{"type":"card",`+
				`"card":{"brand":"visa","last4":"4242"}}}}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(stripeStandIn.Close)
	h := payment.NewHandler(s, gatewayAt(t, stripeStandIn.URL), alwaysPlacedHere{},
		slog.New(slog.DiscardHandler), false)

	number, id, session := openOrder(t, s, 87000, "cardfacts")
	ev := sessionEvent("evt_card_"+uuid.NewString()[:8], session, "paid", 87000)
	sessionField(ev, "payment_intent", "pi_card_probe")
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	header := stripe.GenerateTestSignedPayload(&stripe.UnsignedPayload{Payload: raw, Secret: testWebhookSecret}).Header
	if res := deliver(t, h, raw, header); res.Code != http.StatusOK {
		t.Fatalf("webhook = %d, want 200", res.Code)
	}

	if got := moneyOf(t, ctx, number, id, session); got.status != "succeeded" {
		t.Fatalf("the capture did not land: %+v", got)
	}
	if !expanded.Load() {
		t.Error("the intent was read without expanding its charge")
	}
	var brand, last4, note string
	if err := pool.QueryRow(ctx, `
		SELECT p.card_brand, p.card_last4,
		       (SELECT coalesce(e.note, '') FROM order_events e WHERE e.order_id = p.order_id AND e.kind = 'paid')
		FROM payments p WHERE p.provider_ref = $1`, session).Scan(&brand, &last4, &note); err != nil {
		t.Fatalf("read the payment: %v", err)
	}
	if brand != "visa" || last4 != "4242" || note != "Visa •••• 4242" {
		t.Errorf("card = %q %q, paid note = %q; want visa 4242 and \"Visa •••• 4242\"", brand, last4, note)
	}
}
