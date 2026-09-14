package outbound

import (
	"context"
	"net/http"
	"sync/atomic"
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
	start := time.Now()
	if err := acquire(ctx, t.dep); err != nil {
		outcome := Classify(ctx, op.mutate, err)
		record(op, outcome, time.Since(start), 0)
		return nil, err
	}
	defer release(t.dep)

	attempts := int32(1)
	class := op.class
	perAttempt := perAttemptTimeout(ctx, class)
	req = req.Clone(ctx)
	if perAttempt > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, perAttempt)
		defer cancel()
		req = req.WithContext(ctx)
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		outcome := Classify(ctx, op.mutate, err)
		record(op, outcome, time.Since(start), int(attempts))
		return nil, err
	}
	record(op, OutcomeSucceeded, time.Since(start), int(attempts))
	return resp, nil
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

// instrumentedTransport wraps a base transport and counts attempts for Stripe.
type countingTransport struct {
	dep   Dependency
	base  http.RoundTripper
	count *atomic.Int32
}

func (t *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.count.Add(1)
	tr := &transport{dep: t.dep, base: t.base}
	return tr.RoundTrip(req)
}
