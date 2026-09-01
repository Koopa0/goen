package admin

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

type sessionCloseObservation struct {
	called      bool
	contextErr  error
	hasDeadline bool
	remaining   time.Duration
}

func (o *sessionCloseObservation) ExpireSession(ctx context.Context, _ string) error {
	o.called = true
	o.contextErr = ctx.Err()
	deadline, ok := ctx.Deadline()
	o.hasDeadline = ok
	if ok {
		o.remaining = time.Until(deadline)
	}
	return nil
}

// TestPostCommitSessionExpiryOwnsItsContext proves a disconnected request does
// not cancel cleanup for an already-committed order, while that cleanup still
// receives a finite, short lifetime.
func TestPostCommitSessionExpiryOwnsItsContext(t *testing.T) {
	requestCtx, cancelRequest := context.WithCancel(t.Context())
	cancelRequest()
	if requestCtx.Err() == nil {
		t.Fatal("test request context is not cancelled")
	}

	observed := &sessionCloseObservation{}
	h := &Handler{sessions: observed, log: slog.New(slog.DiscardHandler)}
	h.closeSessions(requestCtx, "GOEN-TEST", []string{"cs_test"})

	if !observed.called {
		t.Fatal("ExpireSession was not called")
	}
	if observed.contextErr != nil {
		t.Errorf("ExpireSession context error = %v; want a context detached from request cancellation",
			observed.contextErr)
	}
	if !observed.hasDeadline {
		t.Fatal("ExpireSession context has no deadline")
	}
	if observed.remaining <= 0 || observed.remaining > 5*time.Second {
		t.Errorf("ExpireSession context had %v remaining; want a live deadline no more than 5s away",
			observed.remaining)
	}
}
