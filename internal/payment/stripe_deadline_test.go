package payment

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	stripe "github.com/stripe/stripe-go/v86"
)

type stripeRequestActivity struct {
	next   http.RoundTripper
	active atomic.Int32
}

func (a *stripeRequestActivity) RoundTrip(req *http.Request) (*http.Response, error) {
	a.active.Add(1)
	res, err := a.next.RoundTrip(req)
	if err != nil {
		a.active.Add(-1)
		return nil, err
	}
	res.Body = &stripeActiveBody{ReadCloser: res.Body, activity: a}
	return res, nil
}

type stripeActiveBody struct {
	io.ReadCloser

	activity *stripeRequestActivity
	closed   atomic.Bool
}

func (b *stripeActiveBody) Close() error {
	err := b.ReadCloser.Close()
	if !b.closed.Swap(true) {
		b.activity.active.Add(-1)
	}
	return err
}

func stripeDeadlineGateway(t *testing.T, partialBody bool) (*Gateway, *stripeRequestActivity, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	var operation atomic.Value
	stop := make(chan struct{})
	srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		requestOperation := r.Method + " " + r.URL.Path
		if requests.Add(1) == 1 {
			operation.Store(requestOperation)
			panic(http.ErrAbortHandler)
		}
		if first := operation.Load().(string); requestOperation != first {
			t.Errorf("retry operation = %q, want the original %q", requestOperation, first)
		}
		if partialBody {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"cs_deadline",`)
			if err := http.NewResponseController(w).Flush(); err != nil {
				t.Error(err)
			}
		}
		select {
		case <-r.Context().Done():
		case <-stop:
		}
		// Returning without headers would synthesize an empty 200 and race cancellation.
		panic(http.ErrAbortHandler)
	}))
	t.Cleanup(func() { close(stop) })
	client := srv.Client()
	activity := &stripeRequestActivity{next: client.Transport}
	client.Transport = stripeTransport{next: activity, limit: stripeReplyLimit}
	client.Timeout = stripeAttemptTimeout
	// This client redirects destinations and trusts its certificate; it
	// proves timing only. Keep the production backend's retry settings.
	gateway, err := NewGateway("sk_test_notarealkey", "whsec_notusedbythesetests", "https://goen.example")
	if err != nil {
		t.Fatal(err)
	}
	backend, ok := gateway.client.V1CheckoutSessions.B.(*stripe.BackendImplementation)
	if !ok {
		t.Fatalf("checkout backend = %T, want stripe backend", gateway.client.V1CheckoutSessions.B)
	}
	backend.HTTPClient = client
	return gateway, activity, &requests
}

var stripeStallModes = []struct {
	name        string
	partialBody bool
}{
	{name: "no headers"},
	{name: "partial body", partialBody: true},
}

var stripeCleanupPaths = []struct {
	name           string
	expireRejected bool
}{
	{name: "retire obsolete session"},
	{name: "expire rejected session", expireRejected: true},
}

func TestStripeRetriesStayWithinTheCleanupDeadline(t *testing.T) {
	for _, mode := range stripeStallModes {
		t.Run(mode.name, func(t *testing.T) {
			for _, path := range stripeCleanupPaths {
				t.Run(path.name, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						gateway, activity, requests := stripeDeadlineGateway(t, mode.partialBody)
						h := &Handler{gateway: gateway}
						req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/orders/ORDER/pay", http.NoBody)
						start := time.Now()
						var err error
						if path.expireRejected {
							err = h.expireRejectedSession(req, &Order{Number: "ORDER", TotalCents: 10000}, "cs_deadline")
						} else {
							err = h.retireObsoleteSession(req, "ORDER", "cs_deadline", 10000)
						}
						if err == nil {
							t.Fatal("cleanup succeeded on a stalled retry")
						}
						if !errors.Is(err, context.DeadlineExceeded) {
							t.Errorf("cleanup error = %v, want deadline exceeded", err)
						}
						if elapsed := time.Since(start); elapsed != 5*time.Second {
							t.Errorf("cleanup took %v, want the shared five-second deadline including retry", elapsed)
						}
						if got := requests.Load(); got != 2 {
							t.Errorf("requests = %d, want the transient failure and one stalled retry", got)
						}
						if got := activity.active.Load(); got != 0 {
							t.Errorf("active requests after the call returned = %d, want 0", got)
						}
						time.Sleep(10 * time.Second)
						synctest.Wait()
						if got := requests.Load(); got != 2 {
							t.Errorf("requests after the deadline = %d, want 2", got)
						}
					})
				})
			}
		})
	}
}

func TestStripeCancellationEndsTheActiveRetryBeforeReturning(t *testing.T) {
	for _, mode := range stripeStallModes {
		t.Run(mode.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				gateway, activity, requests := stripeDeadlineGateway(t, mode.partialBody)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				returned := make(chan error, 1)
				go func() {
					_, _, err := gateway.ResumeSession(ctx, "cs_deadline")
					returned <- err
				}()
				time.Sleep(2 * time.Second)
				synctest.Wait()
				if got := requests.Load(); got != 2 {
					t.Fatalf("requests before cancellation = %d, want an active retry", got)
				}
				start := time.Now()
				cancel()
				if err := <-returned; err == nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("ResumeSession error = %v, want context.Canceled", err)
				}
				if elapsed := time.Since(start); elapsed != 0 {
					t.Errorf("ResumeSession returned %v after cancel, want 0", elapsed)
				}
				if got := activity.active.Load(); got != 0 {
					t.Errorf("active requests after the call returned = %d, want 0", got)
				}
				time.Sleep(10 * time.Second)
				synctest.Wait()
				if got := requests.Load(); got != 2 {
					t.Errorf("requests after cancellation = %d, want 2", got)
				}
			})
		})
	}
}
