//go:build integration

package restore_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/restore"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p
	if err := loadRestoreFixture(context.Background(), pool); err != nil {
		slog.Error("load restore fixture", "error", err)
		stop()
		os.Exit(1)
	}
	code := m.Run()
	stop()
	os.Exit(code)
}

func loadRestoreFixture(ctx context.Context, pool *pgxpool.Pool) error {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("locate restore test file")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	path := filepath.Join(root, "seed", "restore_fixture.sql")
	if !strings.HasPrefix(filepath.Clean(path), filepath.Clean(root)) {
		return fmt.Errorf("restore fixture path %q escapes repository root", path)
	}
	sql, readErr := os.ReadFile(path) //nolint:gosec // G304: path is anchored under the repository root
	if readErr != nil {
		return readErr
	}
	_, execErr := pool.Exec(ctx, string(sql))
	return execErr
}

func TestBusinessManifestCapturesCommerceState(t *testing.T) {
	lines, err := restore.Collect(t.Context(), pool)
	if err != nil {
		t.Fatalf("collect manifest: %v", err)
	}
	if len(lines) == 0 {
		t.Fatal("business manifest came back empty")
	}
	joined := strings.Join(lines, "\n")
	for _, needle := range []string{
		"reservation\tconsumed",
		"reservation\theld",
		"payment\tsucceeded",
		"payment\tcancelled",
		"payment\trequires_reconciliation",
		"refund\tsucceeded",
		"media\t",
		"outbox_pending\temail.order_shipped",
	} {
		if !strings.Contains(joined, needle) {
			t.Errorf("manifest missing %q in:\n%s", needle, joined)
		}
	}
	if strings.Contains(joined, "restore@example.com") || strings.Contains(joined, "0912") {
		t.Error("manifest leaked customer contact material")
	}
}

func TestBusinessManifestRejectsTamperedImageBytes(t *testing.T) {
	ctx := t.Context()
	baseline, err := restore.Collect(ctx, pool)
	if err != nil {
		t.Fatalf("collect baseline: %v", err)
	}
	baselineCounts, err := exactRowCounts(ctx, pool)
	if err != nil {
		t.Fatalf("count baseline rows: %v", err)
	}

	var originalDigest string
	if scanErr := pool.QueryRow(ctx, `
		SELECT digest FROM media_objects
		 WHERE digest = encode(sha256(decode('010203726573746f7265', 'hex')), 'hex')`).
		Scan(&originalDigest); scanErr != nil {
		t.Fatalf("read original digest: %v", scanErr)
	}
	if _, delErr := pool.Exec(ctx, `DELETE FROM media_objects WHERE digest = $1`, originalDigest); delErr != nil {
		t.Fatalf("remove original image: %v", delErr)
	}
	tamperedDigest := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, insErr := pool.Exec(ctx, `
		INSERT INTO media_objects (digest, content_type, byte_size, width, height, bytes)
		VALUES ($1, 'image/png',
		        octet_length(decode('010203726573746f7265ff', 'hex')), 10, 10,
		        decode('010203726573746f7265ff', 'hex'))`, tamperedDigest); insErr != nil {
		t.Fatalf("insert tampered image bytes: %v", insErr)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.WithoutCancel(ctx), `DELETE FROM media_objects WHERE digest = $1`, tamperedDigest)
		_, _ = pool.Exec(context.WithoutCancel(ctx), `
			INSERT INTO media_objects (digest, content_type, byte_size, width, height, bytes)
			VALUES ($1, 'image/png', octet_length(decode('010203726573746f7265', 'hex')), 10, 10,
			        decode('010203726573746f7265', 'hex'))`, originalDigest)
	})

	tampered, err := restore.Collect(ctx, pool)
	if err != nil {
		t.Fatalf("collect tampered manifest: %v", err)
	}
	if restore.Equal(baseline, tampered) {
		t.Fatalf("tampered image bytes did not change the business manifest")
	}
	tamperedCounts, err := exactRowCounts(ctx, pool)
	if err != nil {
		t.Fatalf("count tampered rows: %v", err)
	}
	if baselineCounts != tamperedCounts {
		t.Errorf("row counts changed from %q to %q — the failure must be manifest-only", baselineCounts, tamperedCounts)
	}
}

func TestBusinessManifestRejectsTamperedPaymentAmount(t *testing.T) {
	ctx := t.Context()
	baseline, err := restore.Collect(ctx, pool)
	if err != nil {
		t.Fatalf("collect baseline: %v", err)
	}
	baselineCounts, err := exactRowCounts(ctx, pool)
	if err != nil {
		t.Fatalf("count baseline rows: %v", err)
	}

	if _, tamperErr := pool.Exec(ctx, `
		UPDATE payments
		   SET intended_amount_cents = intended_amount_cents + 1
		 WHERE provider_ref = 'pi_restore_recon'`); tamperErr != nil {
		t.Fatalf("tamper payment amount: %v", tamperErr)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.WithoutCancel(ctx), `
			UPDATE payments
			   SET intended_amount_cents = intended_amount_cents - 1
			 WHERE provider_ref = 'pi_restore_recon'`)
	})

	tampered, err := restore.Collect(ctx, pool)
	if err != nil {
		t.Fatalf("collect tampered manifest: %v", err)
	}
	if restore.Equal(baseline, tampered) {
		t.Fatalf("tampered payment amount did not change the business manifest")
	}
	tamperedCounts, err := exactRowCounts(ctx, pool)
	if err != nil {
		t.Fatalf("count tampered rows: %v", err)
	}
	if baselineCounts != tamperedCounts {
		t.Errorf("row counts changed from %q to %q — the failure must be manifest-only", baselineCounts, tamperedCounts)
	}
}

func TestRecoverableWorkDoesNotDuplicateCompletedEffects(t *testing.T) {
	ctx := t.Context()
	s := outbox.NewStore(pool, slog.New(slog.DiscardHandler))

	var delivered int
	s.Handle("email.order_shipped", func(context.Context, []byte) error {
		delivered++
		return nil
	})

	first, failed, err := s.Drain(ctx)
	if err != nil {
		t.Fatalf("first drain: %v", err)
	}
	if failed != 0 {
		t.Fatalf("first drain failed=%d", failed)
	}
	if first != 1 {
		t.Fatalf("first drain delivered=%d, want the one pending restore message", first)
	}

	second, failed, err := s.Drain(ctx)
	if err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if second != 0 || failed != 0 {
		t.Fatalf("second drain delivered=%d failed=%d, want no further effects", second, failed)
	}
	if delivered != 1 {
		t.Fatalf("handler ran %d times, want exactly one delivery", delivered)
	}
}

func TestRecommendationProjectionCanBeRebuilt(t *testing.T) {
	ctx := t.Context()
	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_copurchases`).Scan(&before); err != nil {
		t.Fatalf("count copurchases before: %v", err)
	}
	if before == 0 {
		t.Fatal("restore fixture should leave a stale copurchase row to rebuild")
	}

	var rebuilt int
	if err := pool.QueryRow(ctx, `SELECT refresh_copurchases()`).Scan(&rebuilt); err != nil {
		t.Fatalf("refresh copurchases: %v", err)
	}
	var after int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_copurchases`).Scan(&after); err != nil {
		t.Fatalf("count copurchases after: %v", err)
	}
	if after == before {
		t.Fatalf("refresh_copurchases left %d rows unchanged", after)
	}
}

func exactRowCounts(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	rows, err := pool.Query(ctx, `
		SELECT c.relname||' '||(xpath('/row/c/text()', query_to_xml(
			format('select count(*) as c from public.%I', c.relname), false, true, '')))[1]::text::bigint
		  FROM pg_class c
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = 'public' AND c.relkind = 'r'
		 ORDER BY 1`)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return "", err
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, "\n"), rows.Err()
}
