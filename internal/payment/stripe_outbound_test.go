package payment

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func checkoutSessionFixture(id, redirectURL string, status stripe.CheckoutSessionStatus) string {
	b, _ := json.Marshal(map[string]any{
		"id": id, "object": "checkout.session", "url": redirectURL, "status": status,
	})
	return string(b)
}

func writeSplitStripeJSON(w http.ResponseWriter, body string, bodyDelay time.Duration) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconvItoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	if bodyDelay > 0 {
		time.Sleep(bodyDelay)
	}
	_, _ = io.WriteString(w, body)
}

func strconvItoa(n int) string {
	return strconv.Itoa(n)
}

func TestReview352HealthySplitResponseSurvivesHeaders(t *testing.T) {
	outbound.ResetAdmission()
	createBody := checkoutSessionFixture("cs_create_split", "", stripe.CheckoutSessionStatusOpen)
	retrieveBody := checkoutSessionFixture("cs_resume_split", "https://checkout.stripe.test/pay", stripe.CheckoutSessionStatusOpen)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions":
			writeSplitStripeJSON(w, createBody, 50*time.Millisecond)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions/cs_resume_split":
			writeSplitStripeJSON(w, retrieveBody, 50*time.Millisecond)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	g, err := GatewayWithClient("sk_test_notreal", "whsec_test", "https://goen.example",
		stripeClientAt(srv.URL))
	if err != nil {
		t.Fatalf("GatewayWithClient: %v", err)
	}

	id, err := g.StartSession(t.Context(), anOrder(), 0)
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	if id != "cs_create_split" {
		t.Fatalf("StartSession() id = %q", id)
	}

	url, status, err := g.ResumeSession(t.Context(), "cs_resume_split")
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	if status != stripe.CheckoutSessionStatusOpen {
		t.Fatalf("ResumeSession() status = %v", status)
	}
	if url != "https://checkout.stripe.test/pay" {
		t.Fatalf("ResumeSession() url = %q", url)
	}
}

func TestReview352ActualRefusalIsNotRecordedAsSuccess(t *testing.T) {
	outbound.ResetAdmission()
	events := recordEvents(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	_, _, err = g.ResumeSession(t.Context(), "cs_refused")
	if err == nil {
		t.Fatal("ResumeSession() succeeded on an explicit refusal")
	}
	if outbound.Classify(t.Context(), false, err) != outbound.OutcomeRefused {
		t.Fatalf("Classify() = %v, want refused", outbound.Classify(t.Context(), false, err))
	}
	if len(*events) != 1 {
		t.Fatalf("recorded %d events, want 1", len(*events))
	}
	if (*events)[0].Outcome != outbound.OutcomeRefused {
		t.Fatalf("recorded outcome = %v, want refused", (*events)[0].Outcome)
	}
}

func recordEvents(t *testing.T) *[]outbound.Event {
	t.Helper()
	var events []outbound.Event
	var mu sync.Mutex
	outbound.Recorder = func(e outbound.Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}
	t.Cleanup(func() { outbound.Recorder = nil })
	return &events
}

func TestAdapterRecorderSuccessRefusalTransportAmbiguousCancelAdmission(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		outbound.ResetAdmission()
		events := recordEvents(t)

		body := checkoutSessionFixture("cs_ok", "https://checkout.stripe.test/pay", stripe.CheckoutSessionStatusOpen)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		}))
		t.Cleanup(srv.Close)

		g, err := GatewayWithClient("sk_test_notreal", "whsec_test", "https://goen.example",
			stripeClientAt(srv.URL))
		if err != nil {
			t.Fatalf("GatewayWithClient: %v", err)
		}
		_, _, err = g.ResumeSession(t.Context(), "cs_ok")
		if err != nil {
			t.Fatalf("ResumeSession() error = %v", err)
		}
		if len(*events) != 1 || (*events)[0].Outcome != outbound.OutcomeSucceeded {
			t.Fatalf("events = %+v, want one success", *events)
		}
	})

	t.Run("transport failure", func(t *testing.T) {
		outbound.ResetAdmission()
		events := recordEvents(t)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("server cannot hijack")
				return
			}
			conn, _, err := hj.Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}))
		t.Cleanup(srv.Close)

		g, err := GatewayWithClient("sk_test_notreal", "whsec_test", "https://goen.example",
			stripeClientAt(srv.URL))
		if err != nil {
			t.Fatalf("GatewayWithClient: %v", err)
		}
		_, err = g.StartSession(t.Context(), anOrder(), 0)
		if err == nil {
			t.Fatal("StartSession() succeeded after dropped response")
		}
		if len(*events) != 1 || (*events)[0].Outcome != outbound.OutcomeAmbiguous {
			t.Fatalf("events = %+v, want one ambiguous", *events)
		}
	})

	t.Run("caller cancel", func(t *testing.T) {
		outbound.ResetAdmission()
		events := recordEvents(t)

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
				t.Fatalf("ResumeSession() error = %v, want context.Canceled", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("lookup did not return after cancellation")
		}
		if len(*events) != 1 || (*events)[0].Outcome != outbound.OutcomeCancelled {
			t.Fatalf("events = %+v, want one cancelled", *events)
		}
	})
}

func TestAdapterRecorderAdmissionFailure(t *testing.T) {
	outbound.ResetAdmission()
	var events []outbound.Event
	var mu sync.Mutex
	outbound.Recorder = func(e outbound.Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}

	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)

	g, err := GatewayWithClient("sk_test_notreal", "whsec_test", "https://goen.example",
		stripeClientAt(srv.URL))
	if err != nil {
		t.Fatalf("GatewayWithClient: %v", err)
	}
	blockCtx, blockCancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	for range outbound.MaxActiveCalls(outbound.Stripe) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = g.ResumeSession(blockCtx, "cs_block")
		}()
	}
	time.Sleep(50 * time.Millisecond)
	shortCtx, shortCancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer shortCancel()
	_, _, err = g.ResumeSession(shortCtx, "cs_admit_fail")
	if err == nil {
		t.Fatal("ResumeSession() succeeded while admission was saturated")
	}
	if outbound.Classify(shortCtx, false, err) != outbound.OutcomeAdmissionRefused {
		t.Fatalf("Classify() = %v, want admission refused", outbound.Classify(shortCtx, false, err))
	}
	mu.Lock()
	var refused bool
	for _, e := range events {
		if e.LogicalKey == "cs_admit_fail" && e.Outcome == outbound.OutcomeAdmissionRefused {
			refused = true
		}
	}
	mu.Unlock()
	if !refused {
		t.Fatalf("events = %+v, want admission refused for cs_admit_fail", events)
	}
	close(block)
	blockCancel()
	wg.Wait()
	outbound.Recorder = nil
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
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		active.Add(-1)
	}))
	t.Cleanup(srv.Close)

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
	close(release)
	wg.Wait()
}

func TestLookupHonoursForegroundBudget(t *testing.T) {
	outbound.ResetAdmission()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)

	g, err := GatewayWithClient("sk_test_notreal", "whsec_test", "https://goen.example",
		stripeClientAt(srv.URL))
	if err != nil {
		t.Fatalf("GatewayWithClient: %v", err)
	}
	start := time.Now()
	_, _, err = g.ResumeSession(t.Context(), "cs_slow")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("ResumeSession() succeeded while the peer never answered")
	}
	if elapsed > outbound.Budget(outbound.ForegroundLookup)+2*time.Second {
		t.Fatalf("lookup was not bounded by the foreground budget: elapsed %v", elapsed)
	}
}
