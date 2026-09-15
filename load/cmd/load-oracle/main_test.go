//go:build integration

package main

import (
	"os"
	"testing"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/load/oracle"
)

func TestReview353AbsentUsefulWorkCannotPass(t *testing.T) {
	t.Parallel()

	pool := dbtest.Pool(t)
	ctx := t.Context()

	var orders int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orders`).Scan(&orders); err != nil {
		t.Fatalf("count orders: %v", err)
	}
	if orders != 0 {
		t.Fatalf("orders = %d, want empty database", orders)
	}

	for _, key := range []string{
		"LOAD_ORACLE_SUCCESS_COUNT",
		"LOAD_ORACLE_TOTAL_COUNT",
		"LOAD_ORACLE_MIN_SUCCESS",
	} {
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
	}

	dbURL := pool.Config().ConnString()
	if code := run(ctx, dbURL, "dependency-failure", oracle.FlashSaleVariantID.String(), 1); code == 0 {
		t.Fatal("degraded-work gate passed with no workload, no recorded requests and no success counts")
	}
}

func TestReview353DegradedWorkRequiresEvidence(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := t.Context()
	dbURL := pool.Config().ConnString()

	t.Run("below floor fails", func(t *testing.T) {
		t.Setenv("LOAD_ORACLE_SUCCESS_COUNT", "1")
		t.Setenv("LOAD_ORACLE_TOTAL_COUNT", "10")
		if code := run(ctx, dbURL, "dependency-failure", oracle.FlashSaleVariantID.String(), 3); code == 0 {
			t.Fatal("expected failure when successes are below the floor")
		}
	})

	t.Run("above floor passes", func(t *testing.T) {
		t.Setenv("LOAD_ORACLE_SUCCESS_COUNT", "5")
		t.Setenv("LOAD_ORACLE_TOTAL_COUNT", "10")
		if code := run(ctx, dbURL, "dependency-failure", oracle.FlashSaleVariantID.String(), 3); code != 0 {
			t.Fatalf("expected pass with observed successes above floor, exit %d", code)
		}
	})

	t.Run("malformed total fails closed", func(t *testing.T) {
		t.Setenv("LOAD_ORACLE_SUCCESS_COUNT", "5")
		t.Setenv("LOAD_ORACLE_TOTAL_COUNT", "not-a-number")
		if code := run(ctx, dbURL, "dependency-failure", oracle.FlashSaleVariantID.String(), 1); code == 0 {
			t.Fatal("expected malformed total count to fail closed")
		}
	})
}
