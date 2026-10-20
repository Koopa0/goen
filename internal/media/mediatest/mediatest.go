//go:build integration

// Package mediatest makes media's storage fail for real in integration suites:
// media's own, and those of the desks that store uploads through it. It
// carries the integration tag, so no production build sees it.
package mediatest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/media"
)

// LockTimeout is how long a write from a NewStore store waits for a lock
// before PostgreSQL gives up with SQLSTATE 55P03.
const LockTimeout = "75ms"

// Writes records the PutMedia statements a NewStore store sends. A test reads
// it to know that a storage failure it caused was a real failed write, and
// that a recovery was a real successful one.
type Writes struct {
	Calls int    // PutMedia statements started
	Code  string // the SQLSTATE the last one failed with, "" if it did not fail in PostgreSQL
	Live  bool   // whether the last one ended with its context still live
}

type writeKey struct{}

// TraceQueryStart implements pgx.QueryTracer.
func (w *Writes) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "-- name: PutMedia :exec\n") {
		w.Calls++
		return context.WithValue(ctx, writeKey{}, true)
	}
	return ctx
}

// TraceQueryEnd implements pgx.QueryTracer.
func (w *Writes) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if put, ok := ctx.Value(writeKey{}).(bool); !ok || !put {
		return
	}
	w.Live = ctx.Err() == nil
	w.Code = ""
	if cause, ok := errors.AsType[*pgconn.PgError](data.Err); ok {
		w.Code = cause.Code
	}
}

// NewStore returns a media store on admin's database, which must be reached as
// the back office's role, as cmd/goen reaches it. The store has one
// connection, waits at most LockTimeout for a lock, and records its writes in
// w. The pool closes when the test ends.
func NewStore(t *testing.T, admin *pgxpool.Pool, w *Writes) *media.Store {
	t.Helper()
	cfg := admin.Config().Copy()
	cfg.MaxConns = 1
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = LockTimeout
	cfg.ConnConfig.Tracer = w
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	var role string
	if err := p.QueryRow(t.Context(), `SELECT current_user`).Scan(&role); err != nil || role != "admin" {
		t.Fatalf("media current_user = %q/%v, want admin", role, err)
	}
	return media.NewStore(p)
}

// HoldWrites locks media_objects in owner's database against writes, so a
// NewStore store's next write waits out LockTimeout and fails with SQLSTATE
// 55P03. The lock lasts until release is called or the test ends.
func HoldWrites(t *testing.T, owner *pgxpool.Pool) (release func()) {
	t.Helper()
	ctx := t.Context()
	tx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Rollback(ctx, tx) })
	if _, err := tx.Exec(ctx, `LOCK TABLE media_objects IN SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	return func() { db.Rollback(ctx, tx) }
}
