//go:build integration

// Package admintest is what the back office's integration suites share. Every
// file carries the integration tag, so no production build and no untagged tool
// sees it.
package admintest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/web"
)

// BackOffice wraps a handler as its route in cmd/goen does, with 2FA off.
var BackOffice = access.New(slog.New(slog.DiscardHandler), nil)

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

// StaffContext creates an admin user in p and returns a context that carries it
// and a request id, as the access wrapper leaves it for a back-office handler.
func StaffContext(t *testing.T, p *pgxpool.Pool) (context.Context, uuid.UUID) {
	t.Helper()
	var id uuid.UUID
	if err := p.QueryRow(t.Context(), `
		INSERT INTO users (email, role, full_name)
		VALUES ('audit-' || gen_random_uuid() || '@goen.invalid', 'admin', '稽核測試')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	ctx := account.WithUser(t.Context(), account.User{ID: id.String(), Role: "admin"})
	return web.WithRequestID(ctx, "req-"+id.String()[:8]), id
}

// AuditRows counts the audit events of one action.
func AuditRows(t *testing.T, p *pgxpool.Pool, action audit.Action) int {
	t.Helper()
	var n int
	if err := p.QueryRow(t.Context(),
		`SELECT count(*) FROM audit_events WHERE action = $1`, string(action)).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}
