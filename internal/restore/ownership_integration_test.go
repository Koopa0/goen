//go:build integration

package restore_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/restore"
)

func TestDumpCopyPreservesAnExistingDestination(t *testing.T) {
	name, destination := sentinelDatabase(t)
	_, _, cleanup, err := restore.DumpCopy(t.Context(), pool.Config().ConnString(), name)
	if cleanup != nil {
		cleanup()
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "42P04" {
		t.Fatalf("existing destination error = %v, want duplicate_database", err)
	}
	assertSentinel(t, destination)
}

func TestDumpCopyRefusesItsSourceWithoutTouchingIt(t *testing.T) {
	name, source := sentinelDatabase(t)
	_, _, cleanup, err := restore.DumpCopy(t.Context(), source, name)
	if cleanup != nil {
		cleanup()
	}
	if err == nil {
		t.Fatal("source-equal destination accepted")
	}
	assertSentinel(t, source)
}

func TestDumpCopyDropsOnlyItsOwnPartialRestore(t *testing.T) {
	ctx := t.Context()
	templateURL, err := url.Parse(pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	templateURL.Path = "/template1"
	template, err := pgx.Connect(ctx, templateURL.String())
	if err != nil {
		t.Fatal(err)
	}
	// A collision inherited by the owned destination lets pg_restore start
	// restoring the real archive, then fail without replacing client tools.
	if _, err = template.Exec(ctx, `CREATE TABLE public.users (sentinel boolean)`); err != nil {
		_ = template.Close(context.WithoutCancel(ctx))
		t.Fatal(err)
	}
	if err = template.Close(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn, cleanupErr := pgx.Connect(context.WithoutCancel(ctx), templateURL.String())
		if cleanupErr != nil {
			t.Error(cleanupErr)
			return
		}
		defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
		if _, cleanupErr = conn.Exec(context.WithoutCancel(ctx), `DROP TABLE public.users`); cleanupErr != nil {
			t.Error(cleanupErr)
		}
	})
	name := "restore_partial_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	_, _, cleanup, err := restore.DumpCopy(ctx, pool.Config().ConnString(), name)
	if cleanup != nil {
		cleanup()
	}
	if err == nil || !strings.Contains(err.Error(), "pg_restore:") {
		t.Fatalf("partial restore error = %v, want pg_restore failure", err)
	}
	var exists bool
	if scanErr := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); scanErr != nil {
		t.Fatal(scanErr)
	}
	if exists {
		t.Fatal("failed restore left its owned destination behind")
	}
}

func sentinelDatabase(t *testing.T) (name, databaseURL string) {
	t.Helper()
	ctx := t.Context()
	name = "restore_sentinel_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := pool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.WithoutCancel(ctx), "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	conn, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if _, err = conn.Exec(ctx, `CREATE TABLE sentinel (value text); INSERT INTO sentinel VALUES ('preserve me')`); err != nil {
		t.Fatal(err)
	}
	return name, u.String()
}

func assertSentinel(t *testing.T, destination string) {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), destination)
	if err != nil {
		t.Fatalf("pre-existing database was removed: %v", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(t.Context())) }()
	var value string
	if err = conn.QueryRow(t.Context(), `SELECT value FROM sentinel`).Scan(&value); err != nil || value != "preserve me" {
		t.Fatalf("pre-existing sentinel changed: value=%q error=%v", value, err)
	}
}

func TestRestoreManifestRejectsKeyedStockCorruption(t *testing.T) {
	ctx := t.Context()
	fixtureName := "restore_fixture_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	fixtureURL, _, fixtureCleanup, err := restore.DumpCopy(ctx, pool.Config().ConnString(), fixtureName)
	if err != nil {
		t.Fatal(err)
	}
	defer fixtureCleanup()
	sourcePool, err := pgxpool.New(ctx, fixtureURL)
	if err != nil {
		t.Fatal(err)
	}
	defer sourcePool.Close()
	secondID := uuid.New()
	if _, err = sourcePool.Exec(ctx, `INSERT INTO product_variants (id, product_id, sku, price_cents, stock_quantity, position) VALUES ($1, '33333333-3333-4333-8333-333333333333', $2, 100, 14, 1)`, secondID, "RESTORE-"+strings.ToUpper(secondID.String()[:8])); err != nil {
		t.Fatal(err)
	}
	movementID := uuid.New()
	if _, err = sourcePool.Exec(ctx, `INSERT INTO inventory_movements (id, variant_id, delta, reason, idempotency_key) VALUES ($1, '44444444-4444-4444-8444-444444444444', 14, 'receipt', $2)`, movementID, movementID.String()); err != nil {
		t.Fatal(err)
	}
	name := "restore_stock_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	copyURL, artifacts, cleanup, err := restore.DumpCopy(ctx, sourcePool.Config().ConnString(), name)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	copyPool, err := pgxpool.New(ctx, copyURL)
	if err != nil {
		t.Fatal(err)
	}
	defer copyPool.Close()
	intact, err := restore.Collect(ctx, copyPool)
	if err != nil || !restore.Equal(artifacts.Manifest, intact) {
		t.Fatalf("intact copy differs: %v", err)
	}
	for _, offsetting := range []bool{false, true} {
		if _, err = copyPool.Exec(ctx, `UPDATE product_variants SET stock_quantity = CASE WHEN id = $1 THEN 15 ELSE $2 END WHERE id IN ($1, $3)`, "44444444-4444-4444-8444-444444444444", map[bool]int{false: 14, true: 13}[offsetting], secondID); err != nil {
			t.Fatal(err)
		}
		counts, countErr := exactRowCounts(ctx, copyPool)
		if countErr != nil || counts != strings.Join(artifacts.RowCounts, "\n") {
			t.Fatalf("corruption changed row counts: %v", countErr)
		}
		tampered, collectErr := restore.Collect(ctx, copyPool)
		if collectErr != nil {
			t.Fatal(collectErr)
		}
		if restore.Equal(artifacts.Manifest, tampered) {
			t.Errorf("restored inventory corruption passed manifest (offsetting=%t)", offsetting)
		}
	}
	if _, err = copyPool.Exec(ctx, `UPDATE product_variants SET stock_quantity = 14 WHERE id IN ('44444444-4444-4444-8444-444444444444', $1)`, secondID); err != nil {
		t.Fatal(err)
	}
	// Corruption is injected only into the owned restored destination. Its
	// append-only trigger normally forbids rewriting ledger history.
	if _, err = copyPool.Exec(ctx, `ALTER TABLE inventory_movements DISABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"inventory_reservations", "inventory_movements"} {
		// Only identifiers from this fixed list enter the query. Moving records
		// between variants preserves every unkeyed aggregate and row count.
		if _, err = copyPool.Exec(ctx, "UPDATE "+pgx.Identifier{table}.Sanitize()+" SET variant_id = $1 WHERE variant_id = '44444444-4444-4444-8444-444444444444'", secondID); err != nil {
			t.Fatal(err)
		}
		counts, countErr := exactRowCounts(ctx, copyPool)
		if countErr != nil || counts != strings.Join(artifacts.RowCounts, "\n") {
			t.Fatalf("%s corruption changed counts: %v", table, countErr)
		}
		tampered, collectErr := restore.Collect(ctx, copyPool)
		if collectErr != nil {
			t.Fatal(collectErr)
		}
		if restore.Equal(artifacts.Manifest, tampered) {
			t.Errorf("restored %s reassignment passed the keyed manifest", table)
		}
		if _, err = copyPool.Exec(ctx, "UPDATE "+pgx.Identifier{table}.Sanitize()+" SET variant_id = '44444444-4444-4444-8444-444444444444' WHERE variant_id = $1", secondID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = copyPool.Exec(ctx, `ALTER TABLE inventory_movements ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	repaired, err := restore.Collect(ctx, copyPool)
	if err != nil || !restore.Equal(artifacts.Manifest, repaired) {
		t.Fatalf("restored correct values did not return to the saved manifest: %v", err)
	}
	var sourceStock int
	if err = sourcePool.QueryRow(ctx, `SELECT stock_quantity FROM product_variants WHERE id = '44444444-4444-4444-8444-444444444444'`).Scan(&sourceStock); err != nil || sourceStock != 14 {
		t.Fatalf("source inventory changed: stock=%d error=%v", sourceStock, err)
	}
}
