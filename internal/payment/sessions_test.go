package payment

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// expiredSession is Stripe's reply to expiring session id.
func expiredSession(id string) string {
	return `{"id":"` + id + `","object":"checkout.session","status":"expired"}`
}

// deadlineRecorder notes the context each request leaves with.
type deadlineRecorder struct {
	ctxErr      error
	hasDeadline bool
	remaining   time.Duration
	requests    int
}

func (d *deadlineRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	d.requests++
	d.ctxErr = req.Context().Err()
	deadline, ok := req.Context().Deadline()
	d.hasDeadline = ok
	if ok {
		d.remaining = time.Until(deadline)
	}
	return http.DefaultTransport.RoundTrip(req)
}

// TestCloseSessionsOwnsContext: Post commit session expiry owns its context. It
// proves a disconnected request does not cancel cleanup for an
// already-committed order, while that cleanup still receives a finite, short
// lifetime.
func TestCloseSessionsOwnsContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, expiredSession("cs_test"))
	}))
	t.Cleanup(srv.Close)
	recorded := &deadlineRecorder{}
	g := gatewayAt(srv.URL, &http.Client{Transport: recorded})

	requestCtx, cancelRequest := context.WithCancel(t.Context())
	cancelRequest()
	if requestCtx.Err() == nil {
		t.Fatal("test request context is not cancelled")
	}

	CloseSessions(requestCtx, g, slog.New(slog.DiscardHandler), "GOEN-TEST", []string{"cs_test"})

	if recorded.requests != 1 {
		t.Fatalf("Stripe received %d requests, want 1", recorded.requests)
	}
	if recorded.ctxErr != nil {
		t.Errorf("expire request context error = %v; want a context detached from request cancellation",
			recorded.ctxErr)
	}
	if !recorded.hasDeadline {
		t.Fatal("expire request context has no deadline")
	}
	if recorded.remaining <= 0 || recorded.remaining > 5*time.Second {
		t.Errorf("expire request context had %v remaining; want a live deadline no more than 5s away",
			recorded.remaining)
	}
}

// TestCloseSessionsExpiresEach: Every session a cancelled order left open is
// expired at Stripe, in the order the order lists them.
func TestCloseSessionsExpiresEach(t *testing.T) {
	g, requests := stripeAt(t, func(c *call) (int, string) {
		id := strings.TrimSuffix(strings.TrimPrefix(c.path, "/v1/checkout/sessions/"), "/expire")
		return http.StatusOK, expiredSession(id)
	})

	CloseSessions(t.Context(), g, slog.New(slog.DiscardHandler), "GOEN-TEST",
		[]string{"cs_first", "cs_second", "cs_third"})

	got := make([]string, 0, len(*requests))
	for _, c := range *requests {
		got = append(got, c.method+" "+c.path)
	}
	want := []string{
		"POST /v1/checkout/sessions/cs_first/expire",
		"POST /v1/checkout/sessions/cs_second/expire",
		"POST /v1/checkout/sessions/cs_third/expire",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Stripe received %q, want %q", got, want)
	}
}

// TestCloseSessionsDisabled: A deployment with no Stripe key opened no session,
// so there is nothing to expire and nothing to report.
func TestCloseSessionsDisabled(t *testing.T) {
	disabled, err := NewGateway("", "", "https://goen.example")
	if err != nil {
		t.Fatalf("NewGateway() error = %v", err)
	}

	for name, g := range map[string]*Gateway{"nil": nil, "no key": disabled} {
		t.Run(name, func(t *testing.T) {
			var logged bytes.Buffer
			CloseSessions(t.Context(), g, slog.New(slog.NewTextHandler(&logged, nil)),
				"GOEN-TEST", []string{"cs_open"})
			if logged.Len() != 0 {
				t.Errorf("logged %q, want nothing", logged.String())
			}
		})
	}
}

// TestCloseSessionsStripeRefuses: Stripe refuses to expire a session that is no
// longer open, and that is a warning, not a stop: the rest of the order's
// sessions are still expired.
func TestCloseSessionsStripeRefuses(t *testing.T) {
	g, requests := stripeAt(t, func(c *call) (int, string) {
		if strings.Contains(c.path, "cs_paid") {
			return http.StatusBadRequest, `{"error":{"type":"invalid_request_error",` +
				`"message":"You may only expire a Checkout Session that is in the open state."}}`
		}
		return http.StatusOK, expiredSession("cs_open")
	})
	var logged bytes.Buffer

	CloseSessions(t.Context(), g, slog.New(slog.NewTextHandler(&logged, nil)),
		"GOEN-TEST", []string{"cs_paid", "cs_open"})

	if len(*requests) != 2 {
		t.Errorf("Stripe received %d requests, want 2: a refusal must not skip the next session", len(*requests))
	}
	for _, want := range []string{"level=WARN", "order_number=GOEN-TEST", "session_id=cs_paid"} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("log %q is missing %q", logged.String(), want)
		}
	}
	if strings.Contains(logged.String(), "session_id=cs_open") {
		t.Errorf("log %q reports the session that expired", logged.String())
	}
}

// TestHoldTTL: Hold TTL includes the whole payment safety budget.
func TestHoldTTL(t *testing.T) {
	t.Parallel()
	if minSessionLifetime != 30*time.Minute {
		t.Errorf("minSessionLifetime = %s, want 30m", minSessionLifetime)
	}
	if sessionStartMargin != time.Minute {
		t.Errorf("sessionStartMargin = %s, want 1m", sessionStartMargin)
	}
	if HoldTTL != PayWindow+minSessionLifetime+sessionStartMargin {
		t.Errorf("HoldTTL = %s, want pay window + Stripe floor + start margin", HoldTTL)
	}
}
