//go:build integration

// Schema conformance. Every rule 001 encodes is exercised against a value it
// must refuse — a CHECK nobody has watched reject something is a comment with
// a syntax — and against a neighbouring value it must accept, so a constraint
// cannot pass by rejecting everything.
//
// Each case runs inside a transaction that is rolled back, so the cases are
// independent and their order does not matter.
package db_test

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
)

var pool *pgxpool.Pool

// TestMain owns the container: starting one per test function would spend
// several seconds each, and every case here rolls back, so they can share.
func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database for the schema suite", "error", err)
		os.Exit(1)
	}
	pool = p

	code := m.Run()
	stop()
	os.Exit(code)
}

func schemaPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return pool
}

// run executes stmt against a rolled-back transaction that already holds the
// fixtures, and reports whether the database accepted it.
func run(t *testing.T, stmt string) error {
	t.Helper()

	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, ferr := tx.Exec(ctx, fixtures); ferr != nil {
		t.Fatalf("load fixtures: %v", ferr)
	}
	_, err = tx.Exec(ctx, stmt)
	return err
}

// TestEveryForeignKeyIsIndexed catches the omission PostgreSQL does not: it
// creates no index for a foreign key, so an unindexed one turns every parent
// delete into a sequential scan of the child and every join into a slow one.
func TestEveryForeignKeyIsIndexed(t *testing.T) {
	rows, err := schemaPool(t).Query(t.Context(), `
		SELECT c.conrelid::regclass::text, c.conname
		FROM pg_constraint c
		WHERE c.contype = 'f'
		  AND connamespace = 'public'::regnamespace
		  AND NOT EXISTS (
			-- The referencing columns must be a PREFIX of some index, not merely
			-- present in one: an index on (b, a) does nothing for a lookup by a.
			-- indkey is an int2vector, and casting one straight to smallint[]
			-- yields an array whose lower bound is 0, so slicing it from 1 quietly
			-- drops the leading column. Going through its text form gives an
			-- ordinary 1-based array.
			SELECT 1 FROM pg_index i
			WHERE i.indrelid = c.conrelid
			  AND i.indpred IS NULL   -- a partial index covers only its own rows
			  AND i.indisvalid AND i.indislive
			  AND (string_to_array(i.indkey::text, ' ')::smallint[])[1:array_length(c.conkey, 1)]
			      = c.conkey::smallint[]
		  )
		ORDER BY 1, 2`)
	if err != nil {
		t.Fatalf("query constraints: %v", err)
	}
	defer rows.Close()

	var missing []string
	for rows.Next() {
		var table, name string
		if err := rows.Scan(&table, &name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		missing = append(missing, table+" ("+name+")")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if len(missing) > 0 {
		t.Errorf("foreign keys with no index to support them:\n  %s", strings.Join(missing, "\n  "))
	}
}
