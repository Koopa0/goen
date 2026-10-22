//go:build integration

package dbtest_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
)

// TestBlockedCurrentDatabase: Blocked reports a lock waiter of the caller's own
// database, and never one of another copy of the template on the same server.
func TestBlockedCurrentDatabase(t *testing.T) {
	ctx := t.Context()
	// Two copies of the template on one server, as two tests of a binary have.
	pool := dbtest.Pool(t)
	cfg := dbtest.Pool(t).Config()
	cfg.ConnConfig.RuntimeParams["application_name"] = "probe-waiter"
	other, openErr := pgxpool.NewWithConfig(ctx, cfg)
	if openErr != nil {
		t.Fatalf("open pool on the other database: %v", openErr)
	}
	t.Cleanup(other.Close)
	if _, tableErr := other.Exec(ctx, `CREATE TABLE t (id integer PRIMARY KEY); INSERT INTO t VALUES (1)`); tableErr != nil {
		t.Fatalf("create table: %v", tableErr)
	}

	holder, beginErr := other.Begin(ctx)
	if beginErr != nil {
		t.Fatalf("begin holder: %v", beginErr)
	}
	t.Cleanup(func() { _ = holder.Rollback(ctx) })
	if _, lockErr := holder.Exec(ctx, `SELECT id FROM t WHERE id = 1 FOR UPDATE`); lockErr != nil {
		t.Fatalf("hold row: %v", lockErr)
	}

	done := make(chan error, 1)
	go func() {
		_, updateErr := other.Exec(ctx, `UPDATE t SET id = 1 WHERE id = 1`)
		done <- updateErr
	}()

	// Positive control: from inside the other database the helper sees the waiter,
	// with and without a blocker filter, so Match{} with a nil BlockedBy
	// matches too.
	var waiter dbtest.Backend
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, ok, err := dbtest.Blocked(ctx, other, dbtest.Match{Application: "probe-waiter"})
		if err != nil {
			t.Fatalf("Blocked in the other database: %v", err)
		}
		if ok {
			waiter = got
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the waiter never blocked in the other database, so the filter test proves nothing")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if waiter.WaitType != "Lock" || len(waiter.BlockedBy) == 0 {
		t.Fatalf("waiter = %+v, want a Lock wait with a blocker", waiter)
	}
	all, err := dbtest.Backends(ctx, other, dbtest.Match{})
	if err != nil {
		t.Fatalf("Backends{} in the other database: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("Match{} with an empty BlockedBy selected no backend in the other database")
	}
	viaBlocker, ok, err := dbtest.Blocked(ctx, other, dbtest.Match{Application: "probe-waiter", BlockedBy: waiter.BlockedBy})
	if err != nil || !ok || viaBlocker.PID != waiter.PID {
		t.Fatalf("Blocked by %v = %+v, %v, %v; want pid %d", waiter.BlockedBy, viaBlocker, ok, err, waiter.PID)
	}

	// The filter: the same question from this test's first database must find nothing.
	if got, ok, err := dbtest.Blocked(ctx, pool, dbtest.Match{Application: "probe-waiter"}); err != nil {
		t.Fatalf("Blocked in the first database: %v", err)
	} else if ok {
		t.Fatalf("Blocked reported pid %d, a waiter of the other database, to a caller in the first: the probe is not filtered by database", got.PID)
	}

	if err := holder.Rollback(ctx); err != nil {
		t.Fatalf("release holder: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("waiter update: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the waiter did not finish after the holder rolled back")
	}
}
