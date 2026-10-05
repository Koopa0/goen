package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestShutdownCancelsWhatOutlivesTheGrace: a request still running when the
// grace ends must be cancelled, or the pool closes that follow wait on its
// connection for as long as it runs.
func TestShutdownCancelsWhatOutlivesTheGrace(t *testing.T) {
	t.Parallel()
	started, cancelled := make(chan struct{}), make(chan struct{})
	srv := &http.Server{
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
			close(cancelled)
		}),
	}
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(l) }()

	go func() {
		req, reqErr := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+l.Addr().String(), http.NoBody)
		if reqErr != nil {
			return
		}
		if resp, getErr := http.DefaultClient.Do(req); getErr == nil {
			_ = resp.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the handler")
	}

	if err := shutdown(srv, 50*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("shutdown with a request outliving the grace = %v, want the grace's DeadlineExceeded", err)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the request still running after the grace was never cancelled")
	}
}
