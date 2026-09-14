package outbound

import (
	"context"
	"time"
)

type operation struct {
	dep        Dependency
	class      Class
	logicalKey string
	mutate     bool
}

type opKey struct{}

// WithOperation bounds ctx to the class budget and tags the call for transport
// recording. logicalKey is the idempotency or claim key when one exists.
func WithOperation(
	ctx context.Context, dep Dependency, class Class, logicalKey string, mutate bool,
) (context.Context, context.CancelFunc) {
	op := operation{dep: dep, class: class, logicalKey: logicalKey, mutate: mutate}
	ctx = context.WithValue(ctx, opKey{}, op)
	return context.WithTimeout(ctx, Budget(class))
}

func operationFrom(ctx context.Context) (operation, bool) {
	op, ok := ctx.Value(opKey{}).(operation)
	return op, ok
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

func record(op operation, outcome Outcome, elapsed time.Duration, attempts int) {
	if Recorder == nil {
		return
	}
	Recorder(Event{
		Dependency: op.dep,
		Class:      op.class,
		Outcome:    outcome,
		Elapsed:    elapsed,
		LogicalKey: op.logicalKey,
		Attempts:   attempts,
	})
}
