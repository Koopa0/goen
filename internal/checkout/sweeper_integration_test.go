//go:build integration

package checkout_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/checkout"
)

func TestTheAttemptSweepKeepsRecentKeys(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)

	plant := func(key, age string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO checkout_attempts (idempotency_key, created_at)
			VALUES ($1, now() - $2::interval)`, key, age); err != nil {
			t.Fatalf("plant %s: %v", key, err)
		}
	}
	old := checkoutAttemptKey("sweep-old-" + uuid.NewString())
	recent := checkoutAttemptKey("sweep-recent-" + uuid.NewString())
	plant(old, "60 days")
	plant(recent, "1 hour")

	if err := s.SweepAttempts(ctx); err != nil {
		t.Fatalf("SweepAttempts: %v", err)
	}

	for key, want := range map[string]bool{old: false, recent: true} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM checkout_attempts WHERE idempotency_key = $1)`,
			key).Scan(&exists); err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		if exists != want {
			t.Errorf("%s: exists = %v, want %v", key, exists, want)
		}
	}
}
