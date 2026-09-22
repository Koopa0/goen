package outbound

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTransportSplitHealthyBodySurvivesHeaders(t *testing.T) {
	ResetAdmission()
	body := `{"ok":true}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	ctx, finish := WithOperation(t.Context(), Stripe, ForegroundLookup, "split", false)
	defer func() { finish(nil) }()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	resp, err := HTTPClient(Stripe).Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(got) != body {
		t.Fatalf("body = %q, want %q", got, body)
	}
}

func TestTransportBodyStallHonoursBudget(t *testing.T) {
	ResetAdmission()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)

	ctx, finish := WithOperation(t.Context(), Stripe, ForegroundLookup, "stall", false)
	defer func() { finish(nil) }()

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	resp, err := HTTPClient(Stripe).Do(req)
	if err == nil {
		_, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr == nil {
			t.Fatal("Do() succeeded while the body never arrived")
		}
		err = readErr
	}
	elapsed := time.Since(start)
	if elapsed > Budget(ForegroundLookup)+2*time.Second {
		t.Fatalf("body stall was not bounded: elapsed %v", elapsed)
	}
	if Classify(t.Context(), false, err) == OutcomeSucceeded {
		t.Fatalf("Classify() = success on stalled body, err = %v", err)
	}
}

func TestTransportEarlyBodyCloseReleasesAdmission(t *testing.T) {
	ResetAdmission()
	var held atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		held.Add(1)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
		held.Add(-1)
	}))
	t.Cleanup(srv.Close)

	ctx, finish := WithOperation(t.Context(), Stripe, ForegroundLookup, "early-close", false)
	defer func() { finish(nil) }()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	resp, err := HTTPClient(Stripe).Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("Body.Close() error = %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for ActiveCalls(Stripe) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if ActiveCalls(Stripe) != 0 {
		t.Fatalf("active calls = %d after early body close", ActiveCalls(Stripe))
	}
}

func TestTransportCallerCancelDuringBodyRead(t *testing.T) {
	ResetAdmission()
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)

	ctx, finish := WithOperation(t.Context(), Stripe, ForegroundLookup, "cancel-body", false)
	ctx, cancel := context.WithCancel(ctx)
	defer func() { finish(context.Canceled) }()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	resp, err := HTTPClient(Stripe).Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("peer never returned headers")
	}
	cancel()
	_, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr == nil {
		t.Fatal("body read succeeded after caller cancellation")
	}
}

func TestTransportAdmissionCapsWithBodiesInFlight(t *testing.T) {
	ResetAdmission()
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
	t.Cleanup(func() { close(release) })

	client := HTTPClient(Stripe)
	var wg sync.WaitGroup
	for range MaxActiveCalls(Stripe) + 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, finish := WithOperation(t.Context(), Stripe, ForegroundLookup, "admit", false)
			defer func() { finish(nil) }()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
			if err != nil {
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			_, _ = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
		}()
	}
	deadline := time.Now().Add(Budget(ForegroundLookup) + time.Second)
	wantMax := MaxActiveCalls(Stripe)
	for int(peak.Load()) < wantMax && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if int(peak.Load()) > wantMax {
		t.Fatalf("peak active calls = %d, want <= %d", peak.Load(), wantMax)
	}
}
