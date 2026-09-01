package cart

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

// sessionCloseObservation records the context at the existing SessionCloser
// boundary; it has no production role.
type sessionCloseObservation struct {
	called      bool
	contextErr  error
	value       string
	hasDeadline bool
	remaining   time.Duration
}

func (o *sessionCloseObservation) ExpireSession(ctx context.Context, _ string) error {
	o.called = true
	o.contextErr = ctx.Err()
	o.value, _ = ctx.Value(struct{ name string }{"trace"}).(string)
	deadline, ok := ctx.Deadline()
	o.hasDeadline = ok
	if ok {
		o.remaining = time.Until(deadline)
	}
	return nil
}

// TestPostCommitSessionExpiryOwnsItsContext proves a disconnected request does
// not cancel cleanup for an already-committed order. Request values survive for
// tracing, while one short deadline bounds the whole provider cleanup batch.
func TestPostCommitSessionExpiryOwnsItsContext(t *testing.T) {
	key := struct{ name string }{"trace"}
	requestCtx := context.WithValue(t.Context(), key, "request-trace")
	requestCtx, cancelRequest := context.WithCancel(requestCtx)
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
		t.Errorf("ExpireSession context error = %v; want request cancellation detached",
			observed.contextErr)
	}
	if observed.value != "request-trace" {
		t.Errorf("ExpireSession context value = %v, want request-trace", observed.value)
	}
	if !observed.hasDeadline {
		t.Fatal("ExpireSession context has no deadline")
	}
	if observed.remaining <= 0 || observed.remaining > 5*time.Second {
		t.Errorf("ExpireSession context had %v remaining; want a live deadline no more than 5s away",
			observed.remaining)
	}
}
