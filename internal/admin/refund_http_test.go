package admin

// The HTTP interface to Stripe: what goen puts on the wire, and what it makes
// of the answers. White-box so the SDK's backend can point at an httptest.Server.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	stripe "github.com/stripe/stripe-go/v86"
)

type refundStripeCall struct {
	method      string
	path        string
	form        url.Values
	idempotency string
	apiVersion  string
	contextDone <-chan struct{}
}

// refundStripeAt returns a StripeRefunder talking to h instead of
// api.stripe.com. Retries are off so every request is visible exactly once.
func refundStripeAt(t *testing.T, h func(*refundStripeCall) (int, string)) (StripeRefunder, *[]refundStripeCall) {
	t.Helper()
	var log []refundStripeCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse Stripe request form: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		call := refundStripeCall{
			method:      r.Method,
			path:        r.URL.Path,
			form:        r.Form.Clone(),
			idempotency: r.Header.Get("Idempotency-Key"),
			apiVersion:  r.Header.Get("Stripe-Version"),
			contextDone: r.Context().Done(),
		}
		log = append(log, call)
		status, reply := h(&call)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)

	noRetries := int64(0)
	backend := stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
		URL: stripe.String(srv.URL), MaxNetworkRetries: &noRetries,
	})
	const testKey = "sk_test_notarealkey"
	return StripeRefunder{
		client: stripe.NewClient(testKey, stripe.WithBackends(&stripe.Backends{
			API: backend, Connect: backend, Uploads: backend,
		})),
	}, &log
}

func TestStripeRefunderExpandsTheCheckoutPaymentIntent(t *testing.T) {
	r, log := refundStripeAt(t, func(*refundStripeCall) (int, string) {
		return http.StatusOK, `{
			"id":"cs_test_order_7",
			"object":"checkout.session",
			"payment_intent":{"id":"pi_customer_paid_7","object":"payment_intent"}
		}`
	})

	got, err := r.PaymentIntentFor(t.Context(), "cs_test_order_7")
	if err != nil {
		t.Fatalf("PaymentIntentFor() error = %v", err)
	}
	if got != "pi_customer_paid_7" {
		t.Errorf("PaymentIntentFor() = %q, want pi_customer_paid_7", got)
	}
	if len(*log) != 1 {
		t.Fatalf("made %d Stripe requests, want exactly 1", len(*log))
	}
	sent := (*log)[0]
	if sent.method != http.MethodGet || sent.path != "/v1/checkout/sessions/cs_test_order_7" {
		t.Errorf("sent %s %s, want GET /v1/checkout/sessions/cs_test_order_7",
			sent.method, sent.path)
	}
	if got := sent.form["expand[0]"]; len(got) != 1 || got[0] != "payment_intent" {
		t.Errorf("form[expand[0]] = %v, want [payment_intent]", got)
	}
	if sent.apiVersion != "2026-08-26.dahlia" {
		t.Errorf("Stripe-Version = %q, want the API reviewed with stripe-go v86.4",
			sent.apiVersion)
	}
}

func TestStripeRefunderFindsAnExistingRequestAcrossRefundPages(t *testing.T) {
	const (
		intentID   = "pi_paid_order_9"
		requestKey = "return:request-9"
	)
	page := 0
	r, log := refundStripeAt(t, func(call *refundStripeCall) (int, string) {
		page++
		switch page {
		case 1:
			return http.StatusOK, `{
				"object":"list","url":"/v1/refunds","has_more":true,
				"data":[{
					"id":"re_newer_other_request","object":"refund","status":"succeeded",
					"metadata":{"goen_request_key":"return:someone-else"}
				}]
			}`
		case 2:
			return http.StatusOK, `{
				"object":"list","url":"/v1/refunds","has_more":false,
				"data":[{
					"id":"re_original_request_9","object":"refund","status":"pending",
					"metadata":{"goen_request_key":"return:request-9"}
				}]
			}`
		default:
			t.Errorf("unexpected Stripe request %d: %s %s", page, call.method, call.path)
			return http.StatusInternalServerError, `{"error":{"message":"unexpected request"}}`
		}
	})

	id, state, err := r.Refund(t.Context(), intentID, requestKey, 8750)
	if err != nil {
		t.Fatalf("Refund() error = %v", err)
	}
	if id != "re_original_request_9" || state != RefundPending {
		t.Errorf("Refund() = (%q, %q), want (re_original_request_9, pending)", id, state)
	}
	if len(*log) != 2 {
		t.Fatalf("made %d Stripe requests, want 2 list pages and no create", len(*log))
	}
	for i, sent := range *log {
		if sent.method != http.MethodGet || sent.path != "/v1/refunds" {
			t.Errorf("request %d sent %s %s, want GET /v1/refunds", i+1, sent.method, sent.path)
		}
		if got := sent.form.Get("payment_intent"); got != intentID {
			t.Errorf("request %d payment_intent = %q, want %q", i+1, got, intentID)
		}
		if sent.apiVersion != "2026-08-26.dahlia" {
			t.Errorf("request %d Stripe-Version = %q, want the API reviewed with stripe-go v86.4",
				i+1, sent.apiVersion)
		}
	}
	if got := (*log)[0].form.Get("starting_after"); got != "" {
		t.Errorf("first page starting_after = %q, want empty", got)
	}
	if got := (*log)[1].form.Get("starting_after"); got != "re_newer_other_request" {
		t.Errorf("second page starting_after = %q, want re_newer_other_request", got)
	}
}

func TestStripeRefunderCreatesTheRequestedRefundIdempotently(t *testing.T) {
	request := 0
	r, log := refundStripeAt(t, func(call *refundStripeCall) (int, string) {
		request++
		switch request {
		case 1:
			return http.StatusOK, `{
				"object":"list","url":"/v1/refunds","has_more":false,"data":[]
			}`
		case 2:
			return http.StatusOK, `{
				"id":"re_created_12","object":"refund","status":"succeeded",
				"metadata":{"goen_request_key":"return:request-12"}
			}`
		default:
			t.Errorf("unexpected Stripe request %d: %s %s", request, call.method, call.path)
			return http.StatusInternalServerError, `{"error":{"message":"unexpected request"}}`
		}
	})

	id, state, err := r.Refund(t.Context(), "pi_paid_order_12", "return:request-12", 12850)
	if err != nil {
		t.Fatalf("Refund() error = %v", err)
	}
	if id != "re_created_12" || state != RefundSucceeded {
		t.Errorf("Refund() = (%q, %q), want (re_created_12, succeeded)", id, state)
	}
	if len(*log) != 2 {
		t.Fatalf("made %d Stripe requests, want one list and one create", len(*log))
	}

	listed, created := (*log)[0], (*log)[1]
	if listed.method != http.MethodGet || listed.path != "/v1/refunds" {
		t.Errorf("first request sent %s %s, want GET /v1/refunds", listed.method, listed.path)
	}
	if got := listed.form.Get("payment_intent"); got != "pi_paid_order_12" {
		t.Errorf("list payment_intent = %q, want pi_paid_order_12", got)
	}
	if created.method != http.MethodPost || created.path != "/v1/refunds" {
		t.Errorf("second request sent %s %s, want POST /v1/refunds", created.method, created.path)
	}
	want := map[string]string{
		"payment_intent":             "pi_paid_order_12",
		"amount":                     "12850",
		"metadata[goen_request_key]": "return:request-12",
	}
	for key, value := range want {
		if got := created.form.Get(key); got != value {
			t.Errorf("create form[%s] = %q, want %q", key, got, value)
		}
	}
	if created.idempotency != "return:request-12" {
		t.Errorf("create Idempotency-Key = %q, want return:request-12", created.idempotency)
	}
	if listed.idempotency != "" {
		t.Errorf("list Idempotency-Key = %q, want empty", listed.idempotency)
	}
	if created.apiVersion != "2026-08-26.dahlia" {
		t.Errorf("create Stripe-Version = %q, want the API reviewed with stripe-go v86.4",
			created.apiVersion)
	}
}

func TestStripeRefunderCancelsTheHTTPCallWithItsContext(t *testing.T) {
	started := make(chan struct{})
	r, _ := refundStripeAt(t, func(call *refundStripeCall) (int, string) {
		close(started)
		<-call.contextDone
		return http.StatusRequestTimeout, `{"error":{"message":"request canceled"}}`
	})

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() {
		_, err := r.PaymentIntentFor(ctx, "cs_test_cancel_me")
		errCh <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("Stripe request did not reach the test server")
	}
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("PaymentIntentFor() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PaymentIntentFor did not return after its context was canceled")
	}
}
