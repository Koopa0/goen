//go:build integration

package recommend_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p
	code := m.Run()
	stop()
	os.Exit(code)
}

// TestASecondRefreshWhileOneRunsDoesNoWork: two processes overlapping across a
// restart must not both rebuild the projection. The first call keeps its
// transaction open, so it holds the advisory lock; the second returns -1 at once
// and, once the first commits, a later call rebuilds as normal.
func TestASecondRefreshWhileOneRunsDoesNoWork(t *testing.T) {
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit

	var first int32
	if err := tx.QueryRow(ctx, `SELECT refresh_copurchases()`).Scan(&first); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if first < 0 {
		t.Fatalf("the first refresh returned %d, want a count of pairs written", first)
	}

	var second int32
	if err := pool.QueryRow(ctx, `SELECT refresh_copurchases()`).Scan(&second); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if second != -1 {
		t.Errorf("a refresh that overlaps one in progress returned %d, want -1 (no work)", second)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var third int32
	if err := pool.QueryRow(ctx, `SELECT refresh_copurchases()`).Scan(&third); err != nil {
		t.Fatalf("third refresh: %v", err)
	}
	if third < 0 {
		t.Errorf("a refresh after the lock was released returned %d, want a rebuild", third)
	}
}
