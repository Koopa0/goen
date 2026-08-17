package payment

// The HTTP interface to Stripe: what goen puts on the wire, and what it makes of
// the answers. White-box so the SDK's own backend injection can point the client
// at an httptest.Server.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	stripe "github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/i18n"
)

// call is one request the SDK made, recorded off the wire.
type call struct {
	method      string
	path        string
	form        url.Values
	idempotency string
}

// stripeAt returns a Gateway talking to h instead of api.stripe.com, and the log
// of what it sent. MaxNetworkRetries is 0 so a refused request is one request.
func stripeAt(t *testing.T, h func(*call) (int, string)) (*Gateway, *[]call) {
	t.Helper()
	var log []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read the request body: %v", err)
			return
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Errorf("parse the form body: %v", err)
			return
		}
		c := call{
			method: r.Method, path: r.URL.Path, form: form,
			idempotency: r.Header.Get("Idempotency-Key"),
		}
		log = append(log, c)
		status, reply := h(&c)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)

	noRetries := int64(0)
	backend := stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
		URL: stripe.String(srv.URL), MaxNetworkRetries: &noRetries,
	})
	// Shaped like Stripe's so the SDK builds an ordinary Authorization header.
	const testKey = "sk_test_notarealkey"
	return &Gateway{ //nolint:gosec // G101: both strings are test fixtures
		client: stripe.NewClient(testKey, stripe.WithBackends(&stripe.Backends{
			API: backend, Connect: backend, Uploads: backend,
		})),
		webhookSecret: "whsec_notusedbythesetests",
		baseURL:       "https://goen.example",
	}, &log
}

// anOrder owes more than its lines, so the remainder line is always exercised.
func anOrder() *Order {
	return &Order{
		Number: "GO-260806-000007",
		Email:  "someone@example.test",
		Lines: []Line{
			{Name: "耳機", Label: "星霧藍", UnitCents: 199900, Quantity: 1},
			{Name: "保護殼", UnitCents: 49900, Quantity: 2},
		},
		// 199900 + 2*49900 = 299700 of lines; 80 of shipping.
		TotalCents:    299780,
		HoldExpiresAt: time.Now().Add(90 * time.Minute),
	}
}

// TestTheSessionRequestCarriesWhatStripeCharges reads the request off the wire.
// The figures are independent literals, never expressions over the fixture, so
// an amount wrong at both ends still fails.
func TestTheSessionRequestCarriesWhatStripeCharges(t *testing.T) {
	g, log := stripeAt(t, func(*call) (int, string) {
		return http.StatusOK, `{"id":"cs_test_created","object":"checkout.session",` +
			`"url":"https://checkout.stripe.test/c/pay/cs_test_created","status":"open"}`
	})

	id, redirect, err := g.StartSession(t.Context(), anOrder(), 0)
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	if id != "cs_test_created" {
		t.Errorf("StartSession() id = %q, want cs_test_created", id)
	}
	if redirect != "https://checkout.stripe.test/c/pay/cs_test_created" {
		t.Errorf("StartSession() url = %q, want Stripe's own", redirect)
	}
	if len(*log) != 1 {
		t.Fatalf("made %d requests, want exactly 1", len(*log))
	}
	sent := (*log)[0]
	if sent.method != http.MethodPost || sent.path != "/v1/checkout/sessions" {
		t.Errorf("sent %s %s, want POST /v1/checkout/sessions", sent.method, sent.path)
	}

	want := map[string]string{
		"mode":                                          "payment",
		"client_reference_id":                           "GO-260806-000007",
		"metadata[order_number]":                        "GO-260806-000007",
		"customer_email":                                "someone@example.test",
		"line_items[0][price_data][currency]":           "twd",
		"line_items[0][price_data][unit_amount]":        "199900",
		"line_items[0][quantity]":                       "1",
		"line_items[0][price_data][product_data][name]": "耳機(星霧藍)",
		"line_items[1][price_data][unit_amount]":        "49900",
		"line_items[1][quantity]":                       "2",
		"line_items[1][price_data][product_data][name]": "保護殼",
		"line_items[2][price_data][unit_amount]":        "80",
		"line_items[2][quantity]":                       "1",
	}
	for k, v := range want {
		if got := sent.form.Get(k); got != v {
			t.Errorf("form[%s] = %q, want %q", k, got, v)
		}
	}

	// Written from the contract and not from the code: money must not arrive
	// after the stock hold the session is bounded by has expired.
	if got := sent.form["payment_method_types[0]"]; len(got) != 1 || got[0] != "card" {
		t.Errorf("form[payment_method_types[0]] = %v, want [card]; without the pin "+
			"the Dashboard may offer a DELAYED method, whose money settles days "+
			"after the session expires — and the stock hold expires WITH the "+
			"session, so the units are back on the shelf before the capture lands", got)
	}
	if got := sent.form["payment_method_types[1]"]; len(got) != 0 {
		t.Errorf("form carries a second payment_method_type = %v; card is what a "+
			"30-minute hold can survive, and anything else is a feature rather "+
			"than a flag", got)
	}
	if sent.idempotency == "" {
		t.Error("no Idempotency-Key header — two POSTs that both read \"no live " +
			"session\" would open two checkouts for one order")
	}
	if sent.idempotency != SessionKey("GO-260806-000007", 299780, 0) {
		t.Errorf("Idempotency-Key = %q, want the key SessionKey derives", sent.idempotency)
	}
}

// TestTheSessionExpiresWithTheStockHold reads expires_at off the wire; it comes
// from the reservation.
func TestTheSessionExpiresWithTheStockHold(t *testing.T) {
	g, log := stripeAt(t, func(*call) (int, string) {
		return http.StatusOK, `{"id":"cs_x","object":"checkout.session","url":"https://x.test","status":"open"}`
	})
	o := anOrder()
	if _, _, err := g.StartSession(t.Context(), o, 0); err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	got := (*log)[0].form.Get("expires_at")
	want := o.SessionExpiry().Unix()
	if got != strconv.FormatInt(want, 10) {
		t.Errorf("expires_at = %s, want %d — the session must die with the hold, "+
			"not thirty minutes from whenever the customer opened the pay page",
			got, want)
	}
}

// TestTheSessionTotalsWhatTheOrderOwes holds the one invariant Stripe's page
// has to satisfy: payments_capture_matches_order refuses a capture that is not
// exactly order_amount_owed, so the line items must sum to it.
//
// Both directions, and the second is the one that mattered. Shipping and tax add
// to the lines; a coupon and store credit take away. StartSession refused a
// negative difference outright, so an order reduced by either could never be
// paid — and free delivery over the advertised threshold means any discount at
// all lands below the lines. The customer's credit was debited and their coupon
// spent by the checkout transaction, so the refusal came after the money.
//
// The previous version of this test asserted the refusal. It was written from
// the implementation, and it had to be wrong for the code to look right.
func TestTheSessionTotalsWhatTheOrderOwes(t *testing.T) {
	for _, tt := range []struct {
		name  string
		owed  int64
		items int
	}{
		// anOrder()'s lines are 199900 + 2*49900 = 299700. Every figure below is
		// a hand-computed literal, never an expression over the fixture, so an
		// amount wrong at both ends still fails.
		{name: "shipping on top", owed: 299780, items: 3},
		{name: "nothing added or taken", owed: 299700, items: 2},
		// A reduced order collapses to ONE line at the owed price. Stripe
		// rejects a negative unit_amount — verified against the real API, which
		// answered 400 "Invalid non-negative integer" to the version that sent
		// the reduction as its own line.
		{name: "a coupon takes it below the lines", owed: 294700, items: 1},
		{name: "store credit takes most of it", owed: 9700, items: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g, log := stripeAt(t, func(*call) (int, string) {
				return http.StatusOK, `{"id":"cs_x","object":"checkout.session","url":"https://x.test","status":"open"}`
			})
			o := anOrder()
			o.TotalCents = tt.owed

			if _, _, err := g.StartSession(t.Context(), o, 0); err != nil {
				t.Fatalf("StartSession() refused an order owing %d: %v", tt.owed, err)
			}
			if len(*log) != 1 {
				t.Fatalf("made %d requests, want 1", len(*log))
			}

			// Sum what actually left the process, from the wire rather than from
			// the code's own arithmetic.
			form := (*log)[0].form
			var total int64
			var n int
			for i := 0; ; i++ {
				amt := form.Get(fmt.Sprintf("line_items[%d][price_data][unit_amount]", i))
				if amt == "" {
					break
				}
				qty := form.Get(fmt.Sprintf("line_items[%d][quantity]", i))
				a, err := strconv.ParseInt(amt, 10, 64)
				if err != nil {
					t.Fatalf("unit_amount %q: %v", amt, err)
				}
				q, err := strconv.ParseInt(qty, 10, 64)
				if err != nil {
					t.Fatalf("quantity %q: %v", qty, err)
				}
				total += a * q
				n++
			}
			if n != tt.items {
				t.Errorf("sent %d line items, want %d", n, tt.items)
			}
			if total != tt.owed {
				t.Errorf("Stripe's page totals %d, want %d — capture_payment records "+
					"what the provider charged and payments_capture_matches_order "+
					"refuses anything but order_amount_owed", total, tt.owed)
			}
		})
	}
}

// TestTheHostedPageFollowsTheVisitorsLanguage covers the one page in the flow
// goen does not control.
func TestTheHostedPageFollowsTheVisitorsLanguage(t *testing.T) {
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		want   string
	}{
		{name: "an English visitor", locale: i18n.En, want: "en"},
		{name: "a Chinese visitor", locale: i18n.ZhHant, want: "zh-TW"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g, log := stripeAt(t, func(*call) (int, string) {
				return http.StatusOK, `{"id":"cs_x","object":"checkout.session","url":"https://x.test","status":"open"}`
			})
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			if _, _, err := g.StartSession(ctx, anOrder(), 0); err != nil {
				t.Fatalf("StartSession() error = %v", err)
			}
			if got := (*log)[0].form.Get("locale"); got != tt.want {
				t.Errorf("locale = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestOnlyAnOpenSessionIsResumable is the rule ResumeSession exists for: never
// `open` for a session with money in flight, and never an error read as "not
// open".
func TestOnlyAnOpenSessionIsResumable(t *testing.T) {
	for _, tt := range []struct {
		name     string
		status   int
		body     string
		wantOpen bool
		wantURL  string
		wantErr  bool
	}{
		{
			name: "still open", status: http.StatusOK,
			body:     `{"id":"cs_1","object":"checkout.session","status":"open","url":"https://checkout.stripe.test/c/pay/cs_1"}`,
			wantOpen: true, wantURL: "https://checkout.stripe.test/c/pay/cs_1",
		},
		{
			name: "complete — the money may already be in flight", status: http.StatusOK,
			body:     `{"id":"cs_1","object":"checkout.session","status":"complete","url":"https://checkout.stripe.test/c/pay/cs_1"}`,
			wantOpen: false,
		},
		{
			name: "expired with the stock hold", status: http.StatusOK,
			body:     `{"id":"cs_1","object":"checkout.session","status":"expired"}`,
			wantOpen: false,
		},
		{
			name: "Stripe could not answer", status: http.StatusInternalServerError,
			body:    `{"error":{"type":"api_error","message":"something went wrong"}}`,
			wantErr: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g, _ := stripeAt(t, func(*call) (int, string) { return tt.status, tt.body })

			redirect, open, err := g.ResumeSession(t.Context(), "cs_1")
			switch {
			case tt.wantErr && err == nil:
				t.Fatal("ResumeSession() returned no error for a Stripe failure — " +
					"a check that failed is not a licence to open a second session")
			case !tt.wantErr && err != nil:
				t.Fatalf("ResumeSession() error = %v", err)
			}
			if open != tt.wantOpen {
				t.Errorf("ResumeSession() open = %v, want %v", open, tt.wantOpen)
			}
			if redirect != tt.wantURL {
				t.Errorf("ResumeSession() url = %q, want %q", redirect, tt.wantURL)
			}
		})
	}
}

// TestCancellingClosesTheCheckoutAtStripe covers the request that closes a
// cancelled order's checkout, whose goods are already back on the shelf.
func TestCancellingClosesTheCheckoutAtStripe(t *testing.T) {
	g, log := stripeAt(t, func(*call) (int, string) {
		return http.StatusOK, `{"id":"cs_open","object":"checkout.session","status":"expired"}`
	})

	if err := g.ExpireSession(t.Context(), "cs_open"); err != nil {
		t.Fatalf("ExpireSession() error = %v", err)
	}
	if len(*log) != 1 {
		t.Fatalf("made %d requests, want exactly 1", len(*log))
	}
	sent := (*log)[0]
	if sent.method != http.MethodPost || sent.path != "/v1/checkout/sessions/cs_open/expire" {
		t.Errorf("sent %s %s, want POST /v1/checkout/sessions/cs_open/expire",
			sent.method, sent.path)
	}
}

// TestStripeDecidesWhetherASessionCanBeClosed is the half that is easy to get
// backwards: a customer who paid two seconds ago looks unpaid from here.
func TestStripeDecidesWhetherASessionCanBeClosed(t *testing.T) {
	g, _ := stripeAt(t, func(*call) (int, string) {
		return http.StatusBadRequest, `{"error":{"type":"invalid_request_error",` +
			`"message":"You may only expire a Checkout Session that is in the open state."}}`
	})

	err := g.ExpireSession(t.Context(), "cs_paid")
	if err == nil {
		t.Fatal("ExpireSession() reported success for a session Stripe refused to " +
			"expire — a paid checkout would read as closed and nobody would look again")
	}
	if !strings.Contains(err.Error(), "cs_paid") {
		t.Errorf("error = %v, want it to name the session so the log line is actionable", err)
	}
}

// TestADisabledGatewayMakesNoRequest covers the keyless deployment, where the
// site still sells and the payment page says payment is not configured.
func TestADisabledGatewayMakesNoRequest(t *testing.T) {
	g, err := NewGateway("", "", "https://goen.example")
	if err != nil {
		t.Fatalf("NewGateway() error = %v", err)
	}
	if g.Enabled() {
		t.Fatal("a gateway with no key reports itself enabled")
	}

	// Each must refuse locally: a nil client reached over the wire panics.
	if _, _, err := g.StartSession(t.Context(), anOrder(), 0); !errors.Is(err, ErrDisabled) {
		t.Errorf("StartSession() error = %v, want ErrDisabled", err)
	}
	if _, _, err := g.ResumeSession(t.Context(), "cs_1"); !errors.Is(err, ErrDisabled) {
		t.Errorf("ResumeSession() error = %v, want ErrDisabled", err)
	}
	if err := g.ExpireSession(t.Context(), "cs_1"); !errors.Is(err, ErrDisabled) {
		t.Errorf("ExpireSession() error = %v, want ErrDisabled", err)
	}
}

// TestTheRequestCarriesTheCallersDeadline proves the context reaches the wire.
func TestTheRequestCarriesTheCallersDeadline(t *testing.T) {
	g, log := stripeAt(t, func(*call) (int, string) {
		return http.StatusOK, `{"id":"cs_1","object":"checkout.session","status":"expired"}`
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := g.ExpireSession(ctx, "cs_1"); err == nil {
		t.Fatal("ExpireSession() succeeded on a cancelled context — the caller's " +
			"context is not reaching the HTTP request")
	}
	if len(*log) != 0 {
		t.Errorf("made %d requests on a cancelled context, want 0", len(*log))
	}
}
