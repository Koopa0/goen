package payment

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	stripe "github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/outbound"
)

func stripeClientAt(baseURL string) *stripe.Client {
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

func TestStartSessionDropsResponseWithinMutationBudget(t *testing.T) {
	outbound.ResetAdmission()
	var accepted atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions" {
			accepted.Store(true)
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("server cannot hijack to drop response")
				return
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}))
	t.Cleanup(srv.Close)

	g, err := GatewayWithClient("sk_test_notreal", "whsec_test", "https://goen.example",
		stripeClientAt(srv.URL))
	if err != nil {
		t.Fatalf("GatewayWithClient: %v", err)
	}
	start := time.Now()
	_, err = g.StartSession(t.Context(), anOrder(), 0)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("StartSession() succeeded after the peer dropped the response")
	}
	if !accepted.Load() {
		t.Fatal("peer never accepted the mutation")
	}
	if elapsed > outbound.Budget(outbound.FinancialMutation)+time.Second {
		t.Errorf("elapsed %v exceeds financial mutation budget %v",
			elapsed, outbound.Budget(outbound.FinancialMutation))
	}
	if outbound.Classify(t.Context(), true, err) != outbound.OutcomeAmbiguous {
		t.Errorf("outcome = %v, want ambiguous", outbound.Classify(t.Context(), true, err))
	}
}

func TestResumeSessionRefusalStaysWithinLookupBudget(t *testing.T) {
	outbound.ResetAdmission()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"refused"}}`)
	}))
	t.Cleanup(srv.Close)

	g, err := GatewayWithClient("sk_test_notreal", "whsec_test", "https://goen.example",
		stripeClientAt(srv.URL))
	if err != nil {
		t.Fatalf("GatewayWithClient: %v", err)
	}
	start := time.Now()
	_, _, err = g.ResumeSession(t.Context(), "cs_refused")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("ResumeSession() succeeded on an explicit refusal")
	}
	if calls.Load() > 2 {
		t.Errorf("made %d attempts, want at most one SDK retry", calls.Load())
	}
	if elapsed > outbound.Budget(outbound.ForegroundLookup)+time.Second {
		t.Errorf("elapsed %v exceeds lookup budget %v", elapsed, outbound.Budget(outbound.ForegroundLookup))
	}
	if outbound.Classify(t.Context(), false, err) != outbound.OutcomeRefused {
		t.Errorf("outcome = %v, want refused", outbound.Classify(t.Context(), false, err))
	}
}

func TestResumeSessionHonoursCancellation(t *testing.T) {
	outbound.ResetAdmission()
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)

	g, err := GatewayWithClient("sk_test_notreal", "whsec_test", "https://goen.example",
		stripeClientAt(srv.URL))
	if err != nil {
		t.Fatalf("GatewayWithClient: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() {
		_, _, err := g.ResumeSession(ctx, "cs_cancel")
		errCh <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("lookup never reached the peer")
	}
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("ResumeSession() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("lookup did not return after cancellation")
	}
}

func TestStripeAdmissionCapsActiveCalls(t *testing.T) {
	outbound.ResetAdmission()
	var active atomic.Int32
	var peak atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := active.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		active.Add(-1)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	g, err := GatewayWithClient("sk_test_notreal", "whsec_test", "https://goen.example",
		stripeClientAt(srv.URL))
	if err != nil {
		t.Fatalf("GatewayWithClient: %v", err)
	}
	var wg sync.WaitGroup
	for range outbound.MaxActiveCalls(outbound.Stripe) + 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = g.ResumeSession(t.Context(), "cs_wait")
		}()
	}
	deadline := time.Now().Add(outbound.Budget(outbound.ForegroundLookup) + time.Second)
	wantMax := outbound.MaxActiveCalls(outbound.Stripe)
	for int(peak.Load()) < wantMax && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if int(peak.Load()) > wantMax {
		t.Errorf("peak active calls = %d, want <= %d", peak.Load(), wantMax)
	}
}

func TestRemovingOperationBudgetWouldLetLookupHang(t *testing.T) {
	t.Parallel()
	if outbound.Budget(outbound.ForegroundLookup) <= 0 {
		t.Fatal("ForegroundLookup budget must bound production lookups")
	}
}
