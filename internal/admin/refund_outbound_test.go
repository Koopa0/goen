package admin

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	stripe "github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/outbound"
)

func refundStripeClientAt(baseURL string) *stripe.Client {
	retries := int64(1)
	backend := stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
		URL:               stripe.String(baseURL),
		MaxNetworkRetries: &retries,
		HTTPClient:        outbound.HTTPClient(outbound.Stripe),
	})
	return stripe.NewClient("sk_test_notreal", stripe.WithBackends(&stripe.Backends{
		API: backend, Connect: backend, Uploads: backend,
	}))
}

func TestRefundRecoversAfterDroppedCreateResponse(t *testing.T) {
	outbound.ResetAdmission()
	const (
		intentID   = "pi_paid"
		requestKey = "return:stable-key"
		amount     = int64(5000)
	)
	var createCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/refunds":
			_, _ = io.WriteString(w, `{"object":"list","has_more":false,"data":[]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/refunds":
			n := createCalls.Add(1)
			if n <= 3 {
				hj, ok := w.(http.Hijacker)
				if !ok {
					return
				}
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
				return
			}
			if r.Header.Get("Idempotency-Key") != requestKey {
				t.Errorf("Idempotency-Key = %q, want %q", r.Header.Get("Idempotency-Key"), requestKey)
			}
			_, _ = fmt.Fprintf(w, `{
				"id":"re_recovered","object":"refund","status":"pending",
				"amount":%d,"currency":"twd",
				"payment_intent":{"id":"pi_paid","object":"payment_intent"},
				"metadata":{"goen_request_key":"return:stable-key"}
			}`, amount)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	r := StripeRefunderWithClient(refundStripeClientAt(srv.URL))
	_, _, err := r.Refund(t.Context(), intentID, requestKey, amount)
	if err == nil {
		t.Fatal("first Refund() succeeded after a dropped create response")
	}
	if outbound.Classify(t.Context(), true, err) != outbound.OutcomeAmbiguous {
		t.Errorf("first outcome = %v, want ambiguous", outbound.Classify(t.Context(), true, err))
	}

	id, state, err := r.Refund(t.Context(), intentID, requestKey, amount)
	if err != nil {
		t.Fatalf("retry Refund() error = %v", err)
	}
	if id != "re_recovered" || state != RefundPending {
		t.Errorf("retry = (%q, %q), want (re_recovered, pending)", id, state)
	}
	if createCalls.Load() != 4 {
		t.Errorf("create calls = %d, want 3 (three dropped attempts then recovery)", createCalls.Load())
	}
}
