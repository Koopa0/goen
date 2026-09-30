//go:build integration

package payment_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

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
