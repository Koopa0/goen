package payment

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	stripe "github.com/stripe/stripe-go/v86"
)

// gatewayOver builds a Gateway exactly as production does, with only Stripe's
// address and the retry count changed, so one refused attempt is one request.
func gatewayOver(t *testing.T, stripeURL string) *Gateway {
	t.Helper()
	noRetries := int64(0)
	g, err := newGateway("sk_test_notarealkey", "whsec_notusedbythesetests", "https://goen.example",
		&stripe.BackendConfig{URL: stripe.String(stripeURL), MaxNetworkRetries: &noRetries})
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	return g
}

const openSessionReply = `{"id":"cs_bounded","object":"checkout.session","status":"open",` +
	`"url":"https://checkout.stripe.com/c/pay/cs_bounded"}`

// TestAStripeReplyPastTheBoundIsRefused holds the read bound on the one
// provider whose SDK reads a reply whole, and holds that the bound fails the
// reply rather than cutting it short. A cut reply is invalid JSON, and the SDK
// quotes the first 500 bytes of invalid JSON into the error goen logs, so a
// cutting bound would log a customer's email. Each oversized reply is valid
// JSON with that email in its leading bytes: only the bound can refuse it, and
// only a failing bound keeps the email out of the error.
func TestAStripeReplyPastTheBoundIsRefused(t *testing.T) {
	const customer = "bound-probe@example.com"
	session := func(pad int) string {
		return `{"id":"cs_bounded","object":"checkout.session","customer_email":"` + customer +
			`","pad":"` + strings.Repeat("x", pad) +
			`","status":"open","url":"https://checkout.stripe.com/c/pay/cs_bounded"}`
	}
	declined := func(pad int) string {
		return `{"error":{"type":"card_error","code":"card_declined",` +
			`"payment_intent":{"id":"pi_bounded","receipt_email":"` + customer +
			`"},"pad":"` + strings.Repeat("x", pad) + `"}}`
	}
	tests := []struct {
		name   string
		status int
		reply  string
		ok     bool
	}{
		{name: "an ordinary reply", status: http.StatusOK, reply: session(0), ok: true},
		{name: "a session past the bound", status: http.StatusOK, reply: session(stripeReplyLimit)},
		{name: "an error past the bound", status: http.StatusPaymentRequired, reply: declined(stripeReplyLimit)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.reply)
			}))
			t.Cleanup(srv.Close)

			redirect, _, err := gatewayOver(t, srv.URL).ResumeSession(t.Context(), "cs_bounded")
			if tt.ok {
				if err != nil || redirect == "" {
					t.Fatalf("ResumeSession() = %q, %v; want the open session", redirect, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ResumeSession() read a %d-byte reply whole and returned %q", len(tt.reply), redirect)
			}
			if strings.Contains(err.Error(), customer) {
				t.Errorf("the error goen would log quotes the reply's customer %q:\n%v", customer, err)
			}
			if _, ok := errors.AsType[*http.MaxBytesError](err); !ok {
				t.Errorf("ResumeSession() error = %v; want the reply failed at the %d-byte bound", err, stripeReplyLimit)
			}
		})
	}
}

// TestAStripeErrorCarriesNoSecretCustomerOrCardFacts holds what a Stripe error
// can put in goen's logs, which record err on every provider call. Stripe may
// embed the PaymentIntent, PaymentMethod, SetupIntent or Source an error
// concerns, and the SDK renders an error as its whole JSON; goen reads only an
// error's type, code and message, so the embedded objects never reach it.
func TestAStripeErrorCarriesNoSecretCustomerOrCardFacts(t *testing.T) {
	const (
		intentSecret = "pi_probe_secret_q7Wm2Lx9"
		setupSecret  = "seti_probe_secret_R4nV8c" //nolint:gosec // G101: a fixture, not a credential
		email        = "stripe-error-probe@example.com"
		fingerprint  = "fpProbeZ3k"
	)
	reply := `{"error":{"type":"card_error","code":"card_declined","message":"Your card was declined.",` +
		`"payment_intent":{"id":"pi_probe","object":"payment_intent","client_secret":"` + intentSecret +
		`","receipt_email":"` + email + `"},` +
		`"payment_method":{"id":"pm_probe","object":"payment_method","billing_details":{"email":"` + email +
		`","name":"Probe Holder"},"card":{"brand":"visa","last4":"9731","exp_month":12,"exp_year":2030,` +
		`"fingerprint":"` + fingerprint + `"}},` +
		`"setup_intent":{"id":"seti_probe","object":"setup_intent","client_secret":"` + setupSecret + `"},` +
		`"source":{"id":"src_probe","object":"source","owner":{"email":"` + email + `"}}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)

	_, _, err := gatewayOver(t, srv.URL).ResumeSession(t.Context(), "cs_bounded")
	if err == nil {
		t.Fatal("ResumeSession() succeeded on a 402")
	}
	text := err.Error()
	for _, leaked := range []string{intentSecret, setupSecret, email, fingerprint, `"last4"`, "Probe Holder"} {
		if strings.Contains(text, leaked) {
			t.Errorf("the Stripe error goen would log carries %q:\n%s", leaked, text)
		}
	}
	stripeErr, ok := errors.AsType[*stripe.Error](err)
	if !ok || stripeErr.Type != stripe.ErrorTypeCard || stripeErr.Code != stripe.ErrorCodeCardDeclined ||
		stripeErr.Msg != "Your card was declined." {
		t.Errorf("the error lost what goen reads from it: %v", err)
	}
}

// TestStripeIsReachedOnlyOverVerifiedTLS holds certificate verification on the
// client that carries the secret key: a server whose certificate no system
// root vouches for is never sent a request.
func TestStripeIsReachedOnlyOverVerifiedTLS(t *testing.T) {
	var served atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, openSessionReply)
	}))
	t.Cleanup(srv.Close)

	// The stand-in answers anybody who trusts its certificate: the refusal
	// below is the client's, not a broken server.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		srv.URL+"/v1/checkout/sessions/cs_bounded", http.NoBody)
	if err != nil {
		t.Fatalf("build the trusted request: %v", err)
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("the stand-in does not answer a client that trusts it: %v", err)
	}
	_ = res.Body.Close()
	served.Store(0)

	if _, _, err := gatewayOver(t, srv.URL).ResumeSession(t.Context(), "cs_bounded"); err == nil {
		t.Fatal("ResumeSession() succeeded against a certificate nothing vouches for")
	}
	if n := served.Load(); n != 0 {
		t.Errorf("the untrusted server received %d requests carrying the secret key", n)
	}
}

// TestEveryStripeClientUsesTheBoundedTransport holds that the client goen hands
// the SDK — finite timeout, bounded replies — is the one under every service
// goen calls. A client the SDK built for itself would bring its own, unbounded
// reader.
func TestEveryStripeClientUsesTheBoundedTransport(t *testing.T) {
	if stripeHTTPClient.Timeout <= 0 || stripeHTTPClient.Timeout > stripeAttemptTimeout {
		t.Fatalf("stripeHTTPClient.Timeout = %v, want a finite bound no longer than %v",
			stripeHTTPClient.Timeout, stripeAttemptTimeout)
	}
	if _, ok := stripeHTTPClient.Transport.(stripeTransport); !ok {
		t.Fatalf("stripeHTTPClient.Transport is %T, want stripeTransport", stripeHTTPClient.Transport)
	}

	c := NewStripeClient("sk_test_notarealkey")
	for name, b := range map[string]stripe.Backend{
		"checkout sessions": c.V1CheckoutSessions.B,
		"refunds":           c.V1Refunds.B,
	} {
		impl, ok := b.(*stripe.BackendImplementation)
		if !ok {
			t.Errorf("the %s backend is %T, want the SDK's own implementation", name, b)
			continue
		}
		if impl.HTTPClient != stripeHTTPClient {
			t.Errorf("the %s backend uses %p, not goen's bounded client", name, impl.HTTPClient)
		}
	}
}
