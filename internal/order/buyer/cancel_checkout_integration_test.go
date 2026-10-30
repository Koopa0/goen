//go:build integration

package buyer_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/order/ordertest"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/shoptime"
)

// endpointSigning signs the stand-in's webhooks; it authenticates nothing real.
const endpointSigning = "whsec_cancelcheckoutfixtureonly"

// checkoutStripe is a Stripe stand-in that knows the Checkout Sessions a test
// gives it and answers 404 for any other, as Stripe does for a session it has
// no record of. A session in the status "stuck" reads as open and cannot be
// expired: Stripe answers the expire call with a server error. It counts every
// request it receives, so a test can show that Stripe was never asked.
type checkoutStripe struct {
	mu       sync.Mutex
	status   map[string]string
	requests int
}

func newCheckoutStripe(t *testing.T) (*checkoutStripe, *payment.Gateway) {
	t.Helper()
	s := &checkoutStripe{status: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	gateway, err := payment.NewGatewayAt("sk_test_notreal", endpointSigning, "https://goen.example", srv.URL)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	return s, gateway
}

func (s *checkoutStripe) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path, expire := strings.CutSuffix(r.URL.Path, "/expire")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++
	// The id written back is the stand-in's own, never the request's.
	var id string
	for known := range s.status {
		if path == "/v1/checkout/sessions/"+known {
			id = known
		}
	}
	status := s.status[id]
	switch {
	case id == "":
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"error":{"type":"invalid_request_error","code":"resource_missing",`+
			`"message":"No such checkout.session"}}`)
		return
	case expire && status == "stuck":
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `{"error":{"type":"api_error","message":"Stripe is unavailable"}}`)
		return
	case expire && status != "open":
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":{"type":"invalid_request_error",`+
			`"message":"Only an open Checkout Session can be expired"}}`)
		return
	case expire:
		status = "expired"
		s.status[id] = status
	case status == "stuck":
		status = "open"
	}
	_, _ = fmt.Fprintf(w, `{"id":%q,"object":"checkout.session","status":%q,`+
		`"url":"https://checkout.stripe.com/c/pay/%s"}`, id, status, id)
}

func (s *checkoutStripe) set(id, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status[id] = status
}

func (s *checkoutStripe) state(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status[id]
}

func (s *checkoutStripe) requested() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

// handlerWithCheckouts is the buyer handler wired as cmd/goen wires it: on the
// store role, with the expirer that asks gateway.
func handlerWithCheckouts(t *testing.T, gateway *payment.Gateway) *buyer.Handler {
	t.Helper()
	appPool := storeRolePool(t)
	return buyer.NewHandler(buyer.NewStore(appPool), access.NewStore(appPool, false),
		slog.New(slog.DiscardHandler), testLimiter(), true).
		WithCheckouts(payment.NewSessionExpirer(payment.NewStore(appPool), gateway))
}

// orderWithCheckout is an unpaid order holding one unit for ten more minutes,
// with a Checkout Session open for all it owes.
func orderWithCheckout(t *testing.T, label string) (number, ref string, variant uuid.UUID) {
	t.Helper()
	variant = ordertest.FreshVariant(t, pool, label)
	orderID := ordertest.HeldOrder(t, pool, variant, -10*time.Minute, false)
	number = ordertest.NumberOf(t, pool, orderID)
	ref = "cs_cancel_checkout_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if _, err := pool.Exec(t.Context(), `SELECT open_payment($1, $2, 100000)`, orderID, ref); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	return number, ref, variant
}

func paymentStatus(t *testing.T, ref string) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(t.Context(),
		`SELECT status FROM payments WHERE provider_ref = $1`, ref).Scan(&status); err != nil {
		t.Fatalf("read payment %s: %v", ref, err)
	}
	return status
}

func cancelRequest(t *testing.T, h *buyer.Handler, locale i18n.Locale, number string) *httptest.ResponseRecorder {
	t.Helper()
	ctx := i18n.WithLocale(t.Context(), locale)
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/orders/"+number+"/cancel", http.NoBody)
	req.SetPathValue("number", number)
	placedBy(t, req, number)
	res := httptest.NewRecorder()
	h.CancelOrder(res, req)
	return res
}

// TestHandler_CancelOrderOpenCheckout: a customer who cancels an order whose
// Checkout Session is open has the session expired at Stripe first, and the
// order is cancelled only when Stripe confirms it. A session that is complete,
// that Stripe will not expire or that it has no record of may yet be paid, so
// nothing is cancelled and the refusal says a payment may be in progress. With
// no Stripe wired in, the order is refused the same way.
func TestHandler_CancelOrderOpenCheckout(t *testing.T) {
	for _, tc := range []struct {
		name     string
		atStripe string
		unwired  bool
		cancels  bool
		// wantStripe is what Stripe is left holding; empty where it knows no session.
		wantStripe string
		wantPay    string
	}{
		{name: "open session", atStripe: "open", cancels: true, wantStripe: "expired", wantPay: "cancelled"},
		{name: "session already expired", atStripe: "expired", cancels: true, wantStripe: "expired", wantPay: "cancelled"},
		{name: "expiry fails", atStripe: "stuck", wantStripe: "stuck", wantPay: "requires_payment"},
		{name: "session completed", atStripe: "complete", wantStripe: "complete", wantPay: "requires_payment"},
		{name: "session unknown to Stripe", wantPay: "requires_payment"},
		{name: "no Stripe wired in", atStripe: "open", unwired: true, wantStripe: "open", wantPay: "requires_payment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stripeAPI, gateway := newCheckoutStripe(t)
			number, ref, variant := orderWithCheckout(t, "cancel-open-checkout")
			if tc.atStripe != "" {
				stripeAPI.set(ref, tc.atStripe)
			}
			appPool := storeRolePool(t)
			h := buyer.NewHandler(buyer.NewStore(appPool), access.NewStore(appPool, false),
				slog.New(slog.DiscardHandler), testLimiter(), true)
			if !tc.unwired {
				h = h.WithCheckouts(payment.NewSessionExpirer(payment.NewStore(appPool), gateway))
			}
			if tc.cancels {
				assertCancelledBehindCheckout(t, h, number, variant)
			} else {
				assertRefusedBehindCheckout(t, h, number, variant)
			}
			if got := paymentStatus(t, ref); got != tc.wantPay {
				t.Errorf("payment is %s, want %s", got, tc.wantPay)
			}
			if got := stripeAPI.state(ref); got != tc.wantStripe {
				t.Errorf("the session is %q at Stripe, want %q", got, tc.wantStripe)
			}
		})
	}
}

// assertCancelledBehindCheckout cancels the order through h and wants it
// cancelled once, its hold released and its stock back.
func assertCancelledBehindCheckout(t *testing.T, h *buyer.Handler, number string, variant uuid.UUID) {
	t.Helper()
	before := ordertest.StockOf(t, pool, variant)
	res := cancelRequest(t, h, i18n.Locales()[0], number)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/orders/"+number+"?cancelled=1" {
		t.Errorf("cancellation = %d %s, want a redirect to the cancelled order", res.Code, res.Header().Get("Location"))
	}
	if f := factsOf(t, number); f.status != "cancelled" || f.heldHolds != 0 || f.events != 1 || f.notices != 1 {
		t.Errorf("cancellation effects: %+v, want cancelled once with the hold released", f)
	}
	if got := ordertest.StockOf(t, pool, variant); got != before+1 {
		t.Errorf("stock after cancellation = %d, want %d", got, before+1)
	}
}

// assertRefusedBehindCheckout asks h to cancel the order in each language and
// wants a 422 that says a payment may be in progress and when the hold ends,
// with the order, its hold and its stock as they were.
func assertRefusedBehindCheckout(t *testing.T, h *buyer.Handler, number string, variant uuid.UUID) {
	t.Helper()
	before := ordertest.StockOf(t, pool, variant)
	until := shoptime.ClockText(holdEnd(t, number))
	for _, locale := range i18n.Locales() {
		localized := i18n.WithLocale(t.Context(), locale)
		want := strings.TrimSpace(fmt.Sprintf(i18n.T(localized, i18n.KeyCancelCheckoutOpenBody),
			fmt.Sprintf(i18n.T(localized, i18n.KeyPayReservedNote), until)))
		res := cancelRequest(t, h, locale, number)
		if res.Code != http.StatusUnprocessableEntity || res.Header().Get("Location") != "" ||
			!strings.Contains(res.Body.String(), want) {
			t.Errorf("%s cancellation = %d %q, want 422 saying a payment may be in progress and when the hold ends",
				locale.Tag(), res.Code, res.Header().Get("Location"))
		}
		if strings.Contains(res.Body.String(), i18n.T(localized, i18n.KeyCancelRefusedBody)) {
			t.Errorf("%s refusal still says the order is paid or shipping", locale.Tag())
		}
	}
	if f := factsOf(t, number); f.status != "pending" || f.heldHolds != 1 || f.events != 0 || f.notices != 0 || f.reversals != 0 {
		t.Errorf("refused cancellation changed the order or its effects: %+v", f)
	}
	if got := ordertest.StockOf(t, pool, variant); got != before {
		t.Errorf("refused cancellation changed stock: %d, want %d", got, before)
	}
}

// holdEnd is when the order's stock hold runs out.
func holdEnd(t *testing.T, number string) time.Time {
	t.Helper()
	var until time.Time
	if err := pool.QueryRow(t.Context(), `
		SELECT min(r.expires_at) FROM inventory_reservations r
		JOIN orders o ON o.id = r.order_id WHERE o.order_number = $1`, number).Scan(&until); err != nil {
		t.Fatalf("read the hold of %s: %v", number, err)
	}
	return until
}

// TestHandler_CancelOrderThenExpiry: the checkout.session.expired Stripe sends
// for the session the cancellation expired is processed and changes nothing:
// the order is cancelled once, its payment stays cancelled and no alarm is
// raised for a person.
func TestHandler_CancelOrderThenExpiry(t *testing.T) {
	ctx := t.Context()
	stripeAPI, gateway := newCheckoutStripe(t)
	number, ref, _ := orderWithCheckout(t, "cancel-then-expiry")
	stripeAPI.set(ref, "open")
	appPool := storeRolePool(t)
	h := buyer.NewHandler(buyer.NewStore(appPool), access.NewStore(appPool, false),
		slog.New(slog.DiscardHandler), testLimiter(), true).
		WithCheckouts(payment.NewSessionExpirer(payment.NewStore(appPool), gateway))
	if res := cancelRequest(t, h, i18n.Locales()[0], number); res.Code != http.StatusSeeOther {
		t.Fatalf("cancellation = %d, want a redirect", res.Code)
	}
	before := factsOf(t, number)

	eventID := "evt_cancel_expiry_" + uuid.NewString()[:12]
	body := fmt.Appendf(nil, `{"id":%q,"object":"event","api_version":%q,"type":"checkout.session.expired",`+
		`"created":%d,"data":{"object":{"id":%q,"object":"checkout.session","status":"expired",`+
		`"payment_status":"unpaid"}}}`, eventID, stripe.APIVersion, time.Now().Unix(), ref)
	signed := stripe.GenerateTestSignedPayload(&stripe.UnsignedPayload{Payload: body, Secret: endpointSigning})
	hook := payment.NewHandler(payment.NewStore(appPool), gateway, access.NewStore(appPool, false),
		slog.New(slog.DiscardHandler))
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/webhooks/stripe", bytes.NewReader(signed.Payload))
	req.Header.Set("Stripe-Signature", signed.Header)
	res := httptest.NewRecorder()
	hook.Webhook(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("the expiry webhook answered %d, want 200", res.Code)
	}

	var processed, flagged bool
	if err := pool.QueryRow(ctx, `
		SELECT processed_at IS NOT NULL, unreconciled IS NOT NULL
		FROM payment_webhook_events WHERE event_id = $1`, eventID).Scan(&processed, &flagged); err != nil {
		t.Fatalf("read the expiry event: %v", err)
	}
	if !processed || flagged {
		t.Errorf("expiry event processed %t, unreconciled %t; want processed and not flagged", processed, flagged)
	}
	if after := factsOf(t, number); after != before {
		t.Errorf("the expiry webhook changed the order: %+v, want %+v", after, before)
	}
	if got := paymentStatus(t, ref); got != "cancelled" {
		t.Errorf("payment is %s, want cancelled", got)
	}
}

// completeBesideCheckout records a completed payment of the order while the
// Checkout Session of ref is still open, so money has arrived beside it.
func completeBesideCheckout(t *testing.T, orderID uuid.UUID, ref string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `SELECT record_complete_payment($1, $2, 100000)`,
		orderID, ref+"_paid"); err != nil {
		t.Fatal(err)
	}
}

// earlierCheckoutNeedsAPerson ends the Checkout Session of ref, opens a second
// and leaves an unresolved event of the first for a person.
func earlierCheckoutNeedsAPerson(t *testing.T, orderID uuid.UUID, ref string) {
	t.Helper()
	// A second session can open only once the first has ended; the event that
	// flags the first arrives after that.
	if _, err := pool.Exec(t.Context(), `SELECT cancel_payment($1)`, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `SELECT open_payment($1, $2, 100000)`,
		orderID, ref+"_second"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO payment_webhook_events (provider, event_id, type, object_ref, payload, processed_at, unreconciled)
		VALUES ('stripe', $1, 'checkout.session.completed', $2, '{}', now(), 'refused_capture: refusal fixture')`,
		"evt_cancel_refusal_"+uuid.NewString(), ref); err != nil {
		t.Fatal(err)
	}
}

// TestHandler_CancelOrderHeldBack: an order with an open Checkout Session beside
// something else that stops the cancellation, money that arrived or an event
// nobody has resolved, is refused with the plain sentence, and Stripe is not
// asked about the session. It stays payable, its payment row stays
// requires_payment, and the order, its hold and its stock are as they were.
func TestHandler_CancelOrderHeldBack(t *testing.T) {
	for _, tc := range []struct {
		name    string
		arrange func(t *testing.T, orderID uuid.UUID, ref string)
	}{
		{name: "another checkout is complete", arrange: completeBesideCheckout},
		{name: "an earlier checkout needs a person", arrange: earlierCheckoutNeedsAPerson},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			stripeAPI, gateway := newCheckoutStripe(t)
			number, ref, variant := orderWithCheckout(t, "cancel-held-back")
			var orderID uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT id FROM orders WHERE order_number = $1`, number).Scan(&orderID); err != nil {
				t.Fatalf("read order %s: %v", number, err)
			}
			tc.arrange(t, orderID, ref)
			var open string
			if err := pool.QueryRow(ctx, `
				SELECT provider_ref FROM payments WHERE order_id = $1 AND status = 'requires_payment'`,
				orderID).Scan(&open); err != nil {
				t.Fatalf("read the open checkout of %s: %v", number, err)
			}
			stripeAPI.set(open, "open")
			h := handlerWithCheckouts(t, gateway)

			before := ordertest.StockOf(t, pool, variant)
			for _, locale := range i18n.Locales() {
				localized := i18n.WithLocale(ctx, locale)
				res := cancelRequest(t, h, locale, number)
				if res.Code != http.StatusUnprocessableEntity || res.Header().Get("Location") != "" ||
					!strings.Contains(res.Body.String(), i18n.T(localized, i18n.KeyCancelRefusedBody)) {
					t.Errorf("%s cancellation = %d %q, want 422 with the plain refusal",
						locale.Tag(), res.Code, res.Header().Get("Location"))
				}
			}
			if n := stripeAPI.requested(); n != 0 {
				t.Errorf("Stripe received %d requests, want none: the order is held back by something else", n)
			}
			if got := stripeAPI.state(open); got != "open" {
				t.Errorf("the session is %q at Stripe, want it left open", got)
			}
			if got := paymentStatus(t, open); got != "requires_payment" {
				t.Errorf("payment is %s, want requires_payment", got)
			}
			if f := factsOf(t, number); f.status != "pending" || f.heldHolds != 1 || f.events != 0 || f.notices != 0 || f.reversals != 0 {
				t.Errorf("refused cancellation changed the order or its effects: %+v", f)
			}
			if got := ordertest.StockOf(t, pool, variant); got != before {
				t.Errorf("refused cancellation changed stock: %d, want %d", got, before)
			}
		})
	}
}

// TestStore_CancelOrderRefusal: CancelOrder answers that a checkout is open only
// when the open Checkout Session is all that stands in the way. A payment that
// has begun, money that arrived or an event somebody has yet to resolve keeps
// the refusal that says nothing of why.
func TestStore_CancelOrderRefusal(t *testing.T) {
	for _, tc := range []struct {
		name     string
		arrange  func(t *testing.T, orderID uuid.UUID, ref string)
		wantOpen bool
	}{
		{
			name:     "a checkout is open",
			arrange:  func(*testing.T, uuid.UUID, string) {},
			wantOpen: true,
		},
		{
			name: "the customer is paying",
			arrange: func(t *testing.T, _ uuid.UUID, ref string) {
				t.Helper()
				setPaymentStatus(t, ref, "processing")
			},
		},
		{
			name: "the payment needs the customer",
			arrange: func(t *testing.T, _ uuid.UUID, ref string) {
				t.Helper()
				setPaymentStatus(t, ref, "requires_action")
			},
		},
		{name: "another checkout is complete", arrange: completeBesideCheckout},
		{
			name: "the checkout is complete",
			arrange: func(t *testing.T, orderID uuid.UUID, ref string) {
				t.Helper()
				if _, err := pool.Exec(t.Context(), `SELECT record_complete_payment($1, $2, 100000)`, orderID, ref); err != nil {
					t.Fatal(err)
				}
			},
		},
		{name: "an earlier checkout needs a person", arrange: earlierCheckoutNeedsAPerson},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			orderID := ordertest.HeldOrder(t, pool, ordertest.FreshVariant(t, pool, "cancel-refusal"), -10*time.Minute, false)
			number := ordertest.NumberOf(t, pool, orderID)
			ref := "cs_cancel_refusal_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
			if _, err := pool.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, ref); err != nil {
				t.Fatal(err)
			}
			tc.arrange(t, orderID, ref)

			err := buyer.NewStore(storeRolePool(t)).CancelOrder(ctx, number)
			if !errors.Is(err, buyer.ErrNotCancellable) {
				t.Fatalf("CancelOrder = %v, want ErrNotCancellable", err)
			}
			open, ok := errors.AsType[*buyer.CheckoutOpenError](err)
			if ok != tc.wantOpen {
				t.Fatalf("CancelOrder = %v, want a checkout-open refusal: %t", err, tc.wantOpen)
			}
			if ok && !open.HoldUntil.After(time.Now()) {
				t.Errorf("refusal names the hold's end as %v, want a time still to come", open.HoldUntil)
			}
		})
	}
}

func setPaymentStatus(t *testing.T, ref, status string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `UPDATE payments SET status = $2 WHERE provider_ref = $1`, ref, status); err != nil {
		t.Fatal(err)
	}
}
