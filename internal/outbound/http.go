package outbound

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"
)

// HTTPClient returns a client whose transport honours ctx budgets, admission
// and cancellation for one dependency. Class is read from [WithOperation].
func HTTPClient(dep Dependency) *http.Client {
	return &http.Client{Transport: &transport{dep: dep, base: http.DefaultTransport}}
}

type transport struct {
	dep  Dependency
	base http.RoundTripper
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	op, tagged := operationFrom(ctx)
	if !tagged {
		op = operation{dep: t.dep, class: ForegroundLookup}
	}
	if meta := metaFrom(ctx); meta != nil {
		meta.tries.Add(1)
	}
	if err := acquire(ctx, t.dep); err != nil {
		return nil, err
	}

	var cancel context.CancelFunc
	class := op.class
	perAttempt := perAttemptTimeout(ctx, class)
	req = req.Clone(ctx)
	if perAttempt > 0 {
		var attemptCancel context.CancelFunc
		ctx, attemptCancel = context.WithTimeout(ctx, perAttempt)
		cancel = attemptCancel
		req = req.WithContext(ctx)
	}

	releaseOnce := func() {
		release(t.dep)
		if cancel != nil {
			cancel()
		}
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		releaseOnce()
		return nil, err
	}
	if resp.Body == nil {
		releaseOnce()
		return resp, nil
	}
	resp.Body = &releaseBody{rc: resp.Body, release: sync.OnceFunc(releaseOnce)}
	return resp, nil
}

type releaseBody struct {
	rc      io.ReadCloser
	release func()
	once    sync.Once
}

func (b *releaseBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if err != nil {
		b.close()
	}
	return n, err
}

func (b *releaseBody) Close() error {
	var err error
	b.once.Do(func() {
		err = b.rc.Close()
		b.release()
	})
	return err
}

func (b *releaseBody) close() {
	if err := b.Close(); err != nil {
		return
	}
}

func perAttemptTimeout(ctx context.Context, class Class) time.Duration {
	budget := Budget(class)
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 && remaining < budget {
			budget = remaining
		}
	}
	// One retry fits inside the class budget for mutations and reconcile reads.
	switch class {
	case FinancialMutation, AsyncReconcile:
		return budget / 2
	default:
		return budget
	}
}

// ActiveCalls returns how many outbound calls are in flight for tests.
func ActiveCalls(dep Dependency) int {
	g := gate(dep)
	return len(g.slots)
}

// MaxActiveCalls reports the admission ceiling for a dependency.
func MaxActiveCalls(dep Dependency) int { return maxActive(dep) }

// SetActiveCallsObserver is test-only: called after each acquire/release.
var SetActiveCallsObserver func(dep Dependency, active int)

func observeActive(dep Dependency) {
	if SetActiveCallsObserver == nil {
		return
	}
	SetActiveCallsObserver(dep, ActiveCalls(dep))
}
