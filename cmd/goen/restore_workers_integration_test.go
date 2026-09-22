//go:build integration

package main

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/restore"
)

type restoreMail struct{ sent atomic.Int32 }

func (m *restoreMail) Send(context.Context, *email.Message) error {
	m.sent.Add(1)
	return nil
}

func TestRestoredWorkersRecoverWithoutRepeatingCompletedEffects(t *testing.T) {
	source := dbtest.Pool(t)
	ctx := t.Context()
	if err := loadRestoreFixtureOn(ctx, source); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(ctx, `UPDATE inventory_reservations
		SET created_at = now() - interval '2 hours', expires_at = now() - interval '1 hour'
		WHERE id = '88880002-0000-4000-8000-000000000002'`); err != nil {
		t.Fatal(err)
	}
	copyName := "restore_workers_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	copyURL, _, cleanup, err := restore.DumpCopy(ctx, source.Config().ConnString(), copyName)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	copyPool, err := pgxpool.New(ctx, copyURL)
	if err != nil {
		t.Fatal(err)
	}
	defer copyPool.Close()
	storePool, err := openPool(ctx, copyURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storePool.Close()
	adminPool, err := openAdminPool(ctx, copyURL)
	if err != nil {
		t.Fatal(err)
	}
	defer adminPool.Close()
	maintenance, err := openMaintenancePool(ctx, copyURL)
	if err != nil {
		t.Fatal(err)
	}
	defer maintenance.Close()
	mail := &restoreMail{}
	for restart := range 2 {
		runCtx, cancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		startWorkers(runCtx, workerDeps{
			pool: storePool, admin: adminPool, maintenance: maintenance,
			log:      slog.New(slog.DiscardHandler),
			notifier: email.New(mail, "http://127.0.0.1", "", ""),
			run:      func(work func()) { wg.Go(work) },
		})
		deadline := time.Now().Add(80 * time.Second)
		settled := false
		for time.Now().Before(deadline) {
			var released, consumed, delivered, pending, stale int
			queryErr := copyPool.QueryRow(ctx, `SELECT
				(SELECT count(*) FROM inventory_reservations WHERE state = 'released'),
				(SELECT count(*) FROM inventory_reservations WHERE state = 'consumed'),
				(SELECT count(*) FROM outbox_messages WHERE delivered_at IS NOT NULL),
				(SELECT count(*) FROM payments WHERE status = 'requires_reconciliation'),
				(SELECT count(*) FROM product_copurchases WHERE other_product_id = '3333aaaa-3333-4333-8333-333333333333')`).Scan(
				&released, &consumed, &delivered, &pending, &stale)
			if queryErr != nil {
				cancel()
				wg.Wait()
				t.Fatal(queryErr)
			}
			if released == 2 && consumed == 1 && delivered == 2 && pending == 1 && stale == 0 && mail.sent.Load() == 1 {
				settled = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		// A second process lifetime must survive an outbox poll before its
		// already-delivered rows can establish that they are not replayed.
		if restart == 1 && settled {
			time.Sleep(3 * time.Second)
		}
		cancel()
		wg.Wait()
		if !settled || mail.sent.Load() != 1 {
			t.Fatalf("restart %d recovered=%t mail sends=%d, want recovered and one total", restart, settled, mail.sent.Load())
		}
	}
	var sourceState string
	if queryErr := source.QueryRow(ctx, `SELECT state FROM inventory_reservations WHERE id = '88880002-0000-4000-8000-000000000002'`).Scan(&sourceState); queryErr != nil {
		t.Fatal(queryErr)
	}
	if sourceState != "held" {
		t.Fatalf("restore worker changed source reservation to %s", sourceState)
	}
}
