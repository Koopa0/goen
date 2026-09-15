package outbound

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type operation struct {
	dep        Dependency
	class      Class
	logicalKey string
	mutate     bool
}

type opKey struct{}

type opMeta struct {
	op    operation
	start time.Time
	tries atomic.Int32
}

type metaKey struct{}

// WithOperation bounds ctx to the class budget and tags the call for transport
// recording. logicalKey is the idempotency or claim key when one exists.
// The returned finish records the semantic outcome once the adapter knows it.
func WithOperation(
	ctx context.Context, dep Dependency, class Class, logicalKey string, mutate bool,
) (ctxOut context.Context, finish func(error)) {
	op := operation{dep: dep, class: class, logicalKey: logicalKey, mutate: mutate}
	meta := &opMeta{op: op, start: time.Now()}
	ctx = context.WithValue(ctx, opKey{}, op)
	ctx = context.WithValue(ctx, metaKey{}, meta)
	ctxOut, cancel := context.WithTimeout(ctx, Budget(class))
	finish = func(err error) {
		cancel()
		record(op, Classify(ctxOut, op.mutate, err), time.Since(meta.start), int(meta.tries.Load()))
	}
	return ctxOut, finish
}

func operationFrom(ctx context.Context) (op operation, ok bool) {
	op, ok = ctx.Value(opKey{}).(operation)
	return op, ok
}

func metaFrom(ctx context.Context) *opMeta {
	meta, ok := ctx.Value(metaKey{}).(*opMeta)
	if !ok {
		return nil
	}
	return meta
}

// Event is one finished outbound call for #332 and tests.
type Event struct {
	Dependency Dependency
	Class      Class
	Outcome    Outcome
	Elapsed    time.Duration
	LogicalKey string
	Attempts   int
}

// Recorder receives finished events when set. Nil is ignored.
var Recorder func(Event)

var recorderMu sync.RWMutex

func record(op operation, outcome Outcome, elapsed time.Duration, attempts int) {
	recorderMu.RLock()
	rec := Recorder
	recorderMu.RUnlock()
	if rec == nil {
		return
	}
	rec(Event{
		Dependency: op.dep,
		Class:      op.class,
		Outcome:    outcome,
		Elapsed:    elapsed,
		LogicalKey: op.logicalKey,
		Attempts:   attempts,
	})
}
