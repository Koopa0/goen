//go:build integration

package campaign_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/campaign"
	"github.com/koopa0/goen/internal/db/dbtest"
)

// A campaign page judges its window in Go, and whether a card is struck follows
// the running_campaigns view in SQL. At both edges of the window they must agree,
// or a page would call a campaign running while its cards show full prices.
// Inside one transaction now() stands still, so a window can start or end
// exactly on it.
func TestStateAtAgreesWithRunningCampaigns(t *testing.T) {
	ctx := t.Context()
	tx, err := dbtest.Pool(t).Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(ctx)) })

	for _, tt := range []struct {
		name   string
		window string // starts_at, ends_at
		want   campaign.State
	}{
		{"starts now", `now(), now() + interval '1 day'`, campaign.Running},
		{"starts a microsecond from now", `now() + interval '1 microsecond', now() + interval '1 day'`, campaign.NotStarted},
		{"ends a microsecond from now", `now() - interval '1 day', now() + interval '1 microsecond'`, campaign.Running},
		{"ends now", `now() - interval '1 day', now()`, campaign.Ended},
	} {
		var (
			id                    uuid.UUID
			now, startsAt, endsAt time.Time
			running               bool
		)
		if err := tx.QueryRow(ctx, `
			INSERT INTO sale_campaigns (slug, title, starts_at, ends_at)
			VALUES ($1, '邊界', `+tt.window+`)
			RETURNING id, now(), starts_at, ends_at`, "edge-"+uuid.NewString()[:8]).Scan(&id, &now, &startsAt, &endsAt); err != nil {
			t.Fatalf("%s: create campaign: %v", tt.name, err)
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM running_campaigns WHERE id = $1)`, id).Scan(&running); err != nil {
			t.Fatalf("%s: read running_campaigns: %v", tt.name, err)
		}
		if want := tt.want == campaign.Running; running != want {
			t.Fatalf("%s: running_campaigns holds the campaign = %v, want %v: the fixture misses the edge", tt.name, running, want)
		}
		if got := campaign.StateAt(startsAt, endsAt, now); got != tt.want {
			t.Errorf("%s: StateAt = %q, want %q, as running_campaigns judges it", tt.name, got, tt.want)
		}
	}
}
