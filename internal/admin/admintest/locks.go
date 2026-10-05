//go:build integration

package admintest

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/pgtx"
)

// LockTimeoutPool is a pool whose statements give up on a row lock after
// 200ms, so a desk's answer to a lock it cannot get is reachable in a test.
func LockTimeoutPool(t *testing.T, pool *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse lock-timeout pool config: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "200"
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open lock-timeout pool: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// HoldRow runs a locking statement in a transaction that stays open until the
// test ends.
func HoldRow(t *testing.T, pool *pgxpool.Pool, stmt string, args ...any) {
	t.Helper()
	ctx := t.Context()
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the lock holder: %v", err)
	}
	t.Cleanup(func() { pgtx.Rollback(ctx, holder) })
	if _, err := holder.Exec(ctx, stmt, args...); err != nil {
		t.Fatalf("hold the row: %v", err)
	}
}

// LockTimedOut reports whether err is PostgreSQL giving up on a lock.
func LockTimedOut(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "55P03"
}
