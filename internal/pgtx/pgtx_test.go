package pgtx

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// rollbackRecorder is a transaction whose Rollback notes the context it was
// given at the moment it was given it: Rollback's own cancel runs on return.
type rollbackRecorder struct {
	pgx.Tx

	called   bool
	err      error
	deadline time.Time
	bounded  bool
}

func (r *rollbackRecorder) Rollback(ctx context.Context) error {
	r.called = true
	r.err = ctx.Err()
	r.deadline, r.bounded = ctx.Deadline()
	return nil
}

func TestRollbackOutlivesTheRequestWithinItsBound(t *testing.T) {
	t.Parallel()
	request, cancel := context.WithCancel(t.Context())
	cancel()

	tx := &rollbackRecorder{}
	Rollback(request, tx)
	latest := time.Now().Add(rollbackTimeout)

	if !tx.called {
		t.Fatal("Rollback(cancelled request) never rolled the transaction back")
	}
	if tx.err != nil {
		t.Errorf("Rollback(cancelled request) rolled back on a context that was already %v", tx.err)
	}
	if !tx.bounded || tx.deadline.After(latest) {
		t.Errorf("Rollback(cancelled request) deadline = %v (set: %t), want one within %v", tx.deadline, tx.bounded, rollbackTimeout)
	}
}
