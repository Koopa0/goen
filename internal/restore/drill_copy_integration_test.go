//go:build integration

package restore_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/restore"
)

func TestRestoreDrillSnapshotManifestSurvivesSourceWrite(t *testing.T) {
	ctx := t.Context()
	copyName := "restore_snap_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	sourceURL := pool.Config().ConnString()
	copyURL, artifacts, cleanup, err := restore.DumpCopy(ctx, sourceURL, copyName)
	if err != nil {
		t.Fatalf("dump and restore copy: %v", err)
	}
	defer cleanup()

	if _, tamperErr := pool.Exec(ctx, `
		UPDATE payments
		   SET intended_amount_cents = intended_amount_cents + 1
		 WHERE provider_ref = 'pi_restore_recon'`); tamperErr != nil {
		t.Fatalf("write live source after snapshot: %v", tamperErr)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.WithoutCancel(ctx), `
			UPDATE payments
			   SET intended_amount_cents = intended_amount_cents - 1
			 WHERE provider_ref = 'pi_restore_recon'`)
	})

	liveAfter, err := restore.Collect(ctx, pool)
	if err != nil {
		t.Fatalf("collect live manifest after write: %v", err)
	}
	if restore.Equal(artifacts.Manifest, liveAfter) {
		t.Fatal("live manifest should diverge from the snapshot artifact after a post-snapshot write")
	}

	copyPool, err := pgxpool.New(ctx, copyURL)
	if err != nil {
		t.Fatalf("open copy pool: %v", err)
	}
	defer copyPool.Close()

	copyManifest, err := restore.Collect(ctx, copyPool)
	if err != nil {
		t.Fatalf("collect copy manifest: %v", err)
	}
	if !restore.Equal(artifacts.Manifest, copyManifest) {
		t.Fatalf("restored copy manifest disagrees with the snapshot artifact:\n%s",
			restore.Diff(artifacts.Manifest, copyManifest))
	}
}

func TestRestoreDrillDestinationTamperFailsManifest(t *testing.T) {
	ctx := t.Context()
	copyName := "restore_tamper_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	copyURL, artifacts, cleanup, err := restore.DumpCopy(ctx, pool.Config().ConnString(), copyName)
	if err != nil {
		t.Fatalf("dump and restore copy: %v", err)
	}
	defer cleanup()

	copyPool, err := pgxpool.New(ctx, copyURL)
	if err != nil {
		t.Fatalf("open copy pool: %v", err)
	}
	defer copyPool.Close()

	if _, tamperErr := copyPool.Exec(ctx, `
		UPDATE payments
		   SET intended_amount_cents = intended_amount_cents + 1
		 WHERE provider_ref = 'pi_restore_recon'`); tamperErr != nil {
		t.Fatalf("tamper restored copy: %v", tamperErr)
	}

	tampered, err := restore.Collect(ctx, copyPool)
	if err != nil {
		t.Fatalf("collect tampered copy manifest: %v", err)
	}
	if restore.Equal(artifacts.Manifest, tampered) {
		t.Fatal("tampered restored copy should fail the business manifest oracle")
	}

	baselineCounts, err := exactRowCounts(ctx, copyPool)
	if err != nil {
		t.Fatalf("count tampered rows: %v", err)
	}
	artifactsCounts := strings.Join(artifacts.RowCounts, "\n")
	if baselineCounts != artifactsCounts {
		t.Errorf("row counts changed — the failure must be manifest-only")
	}
}

func TestRestoredCopyResumesRecoverableWork(t *testing.T) {
	ctx := t.Context()
	copyName := "restore_work_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	copyURL, _, cleanup, err := restore.DumpCopy(ctx, pool.Config().ConnString(), copyName)
	if err != nil {
		t.Fatalf("dump and restore copy: %v", err)
	}
	defer cleanup()

	copyPool, err := pgxpool.New(ctx, copyURL)
	if err != nil {
		t.Fatalf("open copy pool: %v", err)
	}
	defer copyPool.Close()

	var pendingBefore int
	if countErr := copyPool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		 WHERE delivered_at IS NULL AND topic = 'order.shipped'`).
		Scan(&pendingBefore); countErr != nil {
		t.Fatalf("count pending outbox: %v", countErr)
	}
	if pendingBefore != 1 {
		t.Fatalf("pending outbox = %d, want the restore fixture shipment mail", pendingBefore)
	}

	delivered := 0
	s := outbox.NewStore(copyPool, slog.New(slog.DiscardHandler))
	s.Handle(outbox.TopicOrderShipped, func(context.Context, []byte) error {
		delivered++
		return nil
	})
	first, failed, err := s.Drain(ctx)
	if err != nil || failed != 0 || first != 1 {
		t.Fatalf("first drain = delivered %d failed %d err %v", first, failed, err)
	}
	second, failed, err := s.Drain(ctx)
	if err != nil || failed != 0 || second != 0 {
		t.Fatalf("second drain = delivered %d failed %d err %v", second, failed, err)
	}
	if delivered != 1 {
		t.Fatalf("handler ran %d times, want exactly one delivery on the restored copy", delivered)
	}

	if _, err := copyPool.Exec(ctx, `SELECT refresh_copurchases()`); err != nil {
		t.Fatalf("refresh copurchases on restored copy: %v", err)
	}
}

func TestRestoreDrillRolePoolsReachCopy(t *testing.T) {
	ctx := t.Context()
	copyName := "restore_roles_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	sourceURL := pool.Config().ConnString()
	copyURL, _, cleanup, err := restore.DumpCopy(ctx, sourceURL, copyName)
	if err != nil {
		t.Fatalf("dump and restore copy: %v", err)
	}
	defer cleanup()

	password := "drill_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	storeRole := "restore_store_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	adminRole := "restore_admin_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	maintRole := "restore_maint_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	if roleErr := restore.CreateDrillRoles(ctx, copyURL, password, storeRole, adminRole, maintRole); roleErr != nil {
		t.Fatalf("create drill roles: %v", roleErr)
	}
	defer restore.DropDrillRoles(context.WithoutCancel(ctx), copyURL, storeRole, adminRole, maintRole)

	storeURL, err := restore.RolePoolURL(sourceURL, storeRole, password, copyName)
	if err != nil {
		t.Fatalf("store url: %v", err)
	}
	conn, err := pgx.Connect(ctx, storeURL)
	if err != nil {
		t.Fatalf("connect store drill role: %v", err)
	}
	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			t.Errorf("close store drill connection: %v", closeErr)
		}
	}()
	if _, roleErr := conn.Exec(ctx, "SET ROLE store"); roleErr != nil {
		t.Fatalf("assume store: %v", roleErr)
	}
	var dbName, role string
	if err := conn.QueryRow(ctx, "SELECT current_database(), current_user").
		Scan(&dbName, &role); err != nil {
		t.Fatalf("read store session: %v", err)
	}
	if dbName != copyName || role != "store" {
		t.Fatalf("store pool reached %s as %s, want %s as store", dbName, role, copyName)
	}
}
