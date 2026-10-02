//go:build integration

// Package admintest is what the back office's integration suites share. Every
// file carries the integration tag, so no production build and no untagged tool
// sees it.
package admintest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
)

// LoadCatalogue puts the development catalogue into a database dbtest started.
// A suite's TestMain still calls dbtest.Start itself: internal/db refuses a
// TestMain that does not, so no suite applies migrations its own way.
func LoadCatalogue(ctx context.Context, pool *pgxpool.Pool) error {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("admintest: cannot locate this source file")
	}
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "seed", "dev_catalog.sql")
	seed, err := os.ReadFile(path) //nolint:gosec // G304: a fixed path inside this repository
	if err != nil {
		return fmt.Errorf("read seed: %w", err)
	}
	if _, err := pool.Exec(ctx, string(seed)); err != nil {
		return fmt.Errorf("load seed: %w", err)
	}
	return nil
}

// Pool is a catalogued database of one test's own, for a test that counts rows
// the rest of its suite also writes.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := dbtest.Pool(t)
	if err := LoadCatalogue(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}
