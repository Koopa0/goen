//go:build integration

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
)

// TestRollbackCancelledContext: a rollback on a cancelled context fails and
// destroys the pooled connection, while one on a detached context succeeds,
// which is why db.Rollback detaches.
//
// TestRollbackCancelledContext settles which context a deferred
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
func TestRollbackCancelledContext(t *testing.T) {
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

// onePool is a pool with a single connection, so every transaction and every
// later query run on the same backend: a rollback that left the transaction
// open starves the next caller, and one that destroyed the connection shows as
// a different backend pid.
func onePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg := pool.Config().Copy()
	cfg.MinConns, cfg.MaxConns = 0, 1
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open the single-connection pool: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// TestRollbackAbandonedTransaction: db.Rollback ends a transaction whose
// request was cancelled and returns the same backend to the pool idle and not
// in a transaction.
//
// TestRollbackAbandonedTransaction runs db.Rollback itself, not
// pgx's Rollback, against the real server after the request's context is
// cancelled.
//
// The transaction creates a temporary table, which is transactional DDL: it
// exists afterwards only if the transaction committed or was never ended. The
// connection must then be idle (not in a transaction), the same backend, and
// free for the next caller. A Rollback that does nothing leaves the connection
// checked out, so the next acquire times out; one that rolls back on the
// cancelled context makes pgx destroy the connection, so the pid changes.
func TestRollbackAbandonedTransaction(t *testing.T) {
	p := onePool(t)
	ctx, cancel := context.WithCancel(t.Context())

	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// If db.Rollback is broken the transaction still holds the only
	// connection, and closing the pool would wait for it for ever.
	t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(ctx)) })
	var pid int32
	if err = tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("backend pid: %v", err)
	}
	if _, err = tx.Exec(ctx, `CREATE TEMP TABLE rollback_probe (x int)`); err != nil {
		t.Fatalf("create the probe table: %v", err)
	}

	cancel()
	db.Rollback(ctx, tx)

	next, cancelNext := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelNext()
	conn, err := p.Acquire(next)
	if err != nil {
		t.Fatalf("acquire after db.Rollback: %v (the transaction still holds the connection)", err)
	}
	defer conn.Release()

	if status := conn.Conn().PgConn().TxStatus(); status != 'I' {
		t.Errorf("transaction status after db.Rollback = %q, want 'I' (idle)", status)
	}
	var gotPid int32
	var probe *string
	if err := conn.QueryRow(next, `SELECT pg_backend_pid(), to_regclass('pg_temp.rollback_probe')::text`).Scan(&gotPid, &probe); err != nil {
		t.Fatalf("query after db.Rollback: %v", err)
	}
	if gotPid != pid {
		t.Errorf("backend pid after db.Rollback = %d, want %d: the connection was destroyed instead of rolled back cleanly", gotPid, pid)
	}
	if probe != nil {
		t.Errorf("table %s created inside the transaction exists after db.Rollback: the transaction was not rolled back", *probe)
	}
}

// TestRollbackAfterCommit: db.Rollback after Commit changes nothing and leaves
// the connection usable, which is why it can be deferred right after Begin.
func TestRollbackAfterCommit(t *testing.T) {
	p := onePool(t)
	ctx := t.Context()

	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// If db.Rollback is broken the transaction still holds the only
	// connection, and closing the pool would wait for it for ever.
	t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(ctx)) })
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE commit_probe (x int)`); err != nil {
		t.Fatalf("create the probe table: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	db.Rollback(ctx, tx)

	next, cancelNext := context.WithTimeout(ctx, 5*time.Second)
	defer cancelNext()
	var probe *string
	if err := p.QueryRow(next, `SELECT to_regclass('pg_temp.commit_probe')::text`).Scan(&probe); err != nil {
		t.Fatalf("query after Commit and db.Rollback: %v", err)
	}
	if probe == nil {
		t.Error("db.Rollback after Commit undid the committed table")
	}
}
