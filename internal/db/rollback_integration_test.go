//go:build integration

package db_test

import (
	"context"
	"testing"
)

// TestARolledBackTransactionKeepsItsConnection settles which context a deferred
// rollback takes.
//
// pgx's Rollback runs `Exec(ctx, "rollback")` and, when that Exec fails, calls
// conn.die() with the comment "a rollback failure leaves the connection in an
// undefined state" (pgx/v5 tx.go). A cancelled context fails that Exec before
// it reaches the server, so the deferred rollback on a request whose client
// disconnected DESTROYS the pooled connection instead of returning it, and the
// transaction is left for the server to reap. context.WithoutCancel is what
// makes the rollback actually run.
//
// Only the ROLLBACK's own error is asserted: the pool's connection count is the
// visible consequence, but the suite shares one pool, so a neighbouring test
// releasing an idle connection moves that number for unrelated reasons.
func TestARolledBackTransactionKeepsItsConnection(t *testing.T) {
	for _, tt := range []struct {
		name string
		// rollbackCtx builds the context the deferred rollback is given.
		rollbackCtx func(context.Context) context.Context
		wantSame    bool
	}{
		{
			name:        "the live context, once the client has gone",
			rollbackCtx: func(ctx context.Context) context.Context { return ctx },
			wantSame:    false,
		},
		{
			name:        "a context detached from the cancellation",
			rollbackCtx: context.WithoutCancel,
			wantSame:    true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Not parallel: it reads pool-wide counters.
			ctx, cancel := context.WithCancel(t.Context())
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			if _, err := tx.Exec(ctx, `SELECT 1`); err != nil {
				t.Fatalf("exec inside the transaction: %v", err)
			}

			// The customer closes the tab. The handler returns, and the deferred
			// rollback runs against whatever context it was given.
			cancel()
			rollbackErr := tx.Rollback(tt.rollbackCtx(ctx))

			t.Logf("rollback error = %v", rollbackErr)

			if tt.wantSame {
				if rollbackErr != nil {
					t.Errorf("rollback on a detached context failed: %v", rollbackErr)
				}
				return
			}
			if rollbackErr == nil {
				t.Error("rollback on a cancelled context succeeded; " +
					"the premise of the WithoutCancel sites no longer holds and " +
					"they can be normalised to the plain context")
			}
		})
	}
}
