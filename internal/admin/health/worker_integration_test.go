//go:build integration

package health_test

import (
	"log/slog"
	"testing"

	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/outbox"
)

func TestHealthIsDerivedFromTheWorkNotFromAHeartbeat(t *testing.T) {
	ctx := t.Context()
	s := health.NewStore(pool)

	if _, err := pool.Exec(ctx, `DELETE FROM outbox_messages`); err != nil {
		t.Fatalf("clear outbox: %v", err)
	}
	clean, err := s.WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if !clean.OutboxHealthy() {
		t.Errorf("an empty outbox reads as unhealthy: %s", clean.OutboxText(ctx))
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO outbox_messages (topic, dedupe_key, payload, attempts, available_at)
		VALUES ('test.health', 'health-stuck', '{}'::jsonb, 99, now())`); err != nil {
		t.Fatalf("insert stuck: %v", err)
	}
	stuck, stuckErr := s.WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if stuckErr != nil {
		t.Fatalf("health: %v", stuckErr)
	}
	if stuck.OutboxHealthy() {
		t.Error("a message that has run out of attempts reads as healthy")
	}
	if stuck.OutboxStuck != 1 {
		t.Errorf("%d stuck messages, want 1", stuck.OutboxStuck)
	}
}

func TestAMessageWaitingOnItsBackoffIsNotLate(t *testing.T) {
	ctx := t.Context()
	s := health.NewStore(pool)

	if _, err := pool.Exec(ctx, `DELETE FROM outbox_messages`); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO outbox_messages (topic, dedupe_key, payload, attempts, available_at)
		VALUES ('test.health', 'health-backoff', '{}'::jsonb, 1, now() + interval '1 hour')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	view, err := s.WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if view.OutboxPending != 1 {
		t.Errorf("%d pending, want 1", view.OutboxPending)
	}
	if view.OutboxOldest != 0 {
		t.Errorf("a message not yet due reads as %v overdue", view.OutboxOldest)
	}
	if !view.OutboxHealthy() {
		t.Errorf("a message waiting on its backoff reads as unhealthy: %s", view.OutboxText(ctx))
	}
}

func TestNeverRebuiltIsNotTheSameAsJustRebuilt(t *testing.T) {
	ctx := t.Context()
	s := health.NewStore(pool)

	if _, err := pool.Exec(ctx, `DELETE FROM copurchase_refreshes`); err != nil {
		t.Fatalf("clear: %v", err)
	}
	never, err := s.WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if never.CopurchaseEverBuilt {
		t.Error("a projection that never rebuilt reports itself as built")
	}
	if never.RecommendHealthy() {
		t.Error("a projection that has never been rebuilt reads as healthy")
	}

	// A rebuild that found no pair of products leaves the projection empty and
	// is still a rebuild.
	if _, err := pool.Exec(ctx, `SELECT refresh_copurchases()`); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM product_copurchases`); err != nil {
		t.Fatalf("empty the projection: %v", err)
	}
	fresh, freshErr := s.WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if freshErr != nil {
		t.Fatalf("health: %v", freshErr)
	}
	if !fresh.CopurchaseEverBuilt || !fresh.RecommendHealthy() {
		t.Errorf("a just-rebuilt projection reads as %s", fresh.RecommendText(ctx))
	}
}
