//go:build integration

package newsletter_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/newsletter"
)

// storeRolePool connects as the role the worker's pool is, so a missing
// privilege shows here and not first in production.
func storeRolePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse store-role pool config: %v", err)
	}
	cfg.MaxConns = 2
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, execErr := conn.Exec(ctx, `SET ROLE store`)
		return execErr
	}
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open store-role pool: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// request inserts an unanswered request made ageDays ago, with the link already
// dead as it would be after ConfirmTTL.
func request(t *testing.T, email string, ageDays int) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO newsletter_confirmations (email, digest, created_at, expires_at)
		VALUES ($1, sha256(uuid_send(gen_random_uuid())),
		        now() - make_interval(days => $2),
		        now() - make_interval(days => $2) + interval '48 hours')`,
		email, ageDays); err != nil {
		t.Fatalf("insert confirmation request: %v", err)
	}
}

// subscriberSince inserts a subscription confirmed ageDays ago; optOut makes it
// an unsubscribed row, the record that stops the list emailing the address.
func subscriberSince(t *testing.T, email string, ageDays int, optOut bool) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO newsletter_subscribers (email, unsubscribe_token, confirmed_at, unsubscribed_at, created_at)
		VALUES ($1, $2, now() - make_interval(days => $3),
		        CASE WHEN $4 THEN now() - make_interval(days => $3) END,
		        now() - make_interval(days => $3))`,
		email, uuid.NewString()+uuid.NewString(), ageDays, optOut); err != nil {
		t.Fatalf("insert subscriber: %v", err)
	}
}

// TestStore_SweepConfirmationsOldOnly: the confirmation sweep deletes only old
// unanswered requests, never a subscriber or an opt-out.
func TestStore_SweepConfirmationsOldOnly(t *testing.T) {
	t.Parallel()
	stale, justInside, fresh := addr(t), addr(t), addr(t)
	subscriber, optedOut := addr(t), addr(t)
	request(t, stale, 15)
	request(t, justInside, 13)
	request(t, fresh, 0)
	subscriberSince(t, subscriber, 500, false)
	subscriberSince(t, optedOut, 500, true)

	n, err := newsletter.NewStore(storeRolePool(t)).SweepConfirmations(t.Context())
	if err != nil {
		t.Fatalf("SweepConfirmations: %v", err)
	}
	if n < 1 {
		t.Errorf("deleted %d requests, want at least the stale one", n)
	}

	if got := pendingConfirmations(t, stale); got != 0 {
		t.Errorf("a request from 15 days ago survived: %d rows", got)
	}
	if got := pendingConfirmations(t, justInside); got != 1 {
		t.Errorf("a request from 13 days ago: %d rows, want 1", got)
	}
	if got := pendingConfirmations(t, fresh); got != 1 {
		t.Errorf("a request from today: %d rows, want 1", got)
	}
	// The dangerous direction: a subscription, and the opt-out that keeps the
	// list from emailing somebody again, are never swept however old.
	if onList, known := subscribed(t, subscriber); !known || !onList {
		t.Errorf("a 500-day-old subscriber: known=%v onList=%v, want both true", known, onList)
	}
	if onList, known := subscribed(t, optedOut); !known || onList {
		t.Errorf("a 500-day-old opt-out: known=%v onList=%v, want known and off the list", known, onList)
	}
}
