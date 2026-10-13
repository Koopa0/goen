//go:build integration

package health_test

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/db/dbtest"
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

func TestStaffTaskCountDeduplicatesPaymentsAndExceedsTheVisibleSample(t *testing.T) {
	fixturePool := dbtest.Pool(t)
	if err := admintest.LoadCatalogue(t.Context(), fixturePool); err != nil {
		t.Fatalf("load isolated catalogue: %v", err)
	}
	ctx, actor := admintest.StaffContext(t, fixturePool)
	store := health.NewStore(fixturePool)
	messages := outbox.NewStore(fixturePool, slog.New(slog.DiscardHandler))
	before, err := store.WorkerHealth(ctx, messages)
	if err != nil {
		t.Fatalf("health before fixtures: %v", err)
	}
	baseline, err := store.StaffTaskCount(ctx)
	if err != nil {
		t.Fatalf("count before fixtures: %v", err)
	}
	var firstRef string
	for i := range 2 {
		number := admintest.PlaceUnpaidOrder(t, fixturePool)
		ref := "cs_priority_" + uuid.NewString()
		if _, paymentErr := fixturePool.Exec(ctx, `
			INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents)
			SELECT id, $2, 'requires_reconciliation', 500000 FROM orders WHERE order_number = $1`, number, ref); paymentErr != nil {
			t.Fatalf("record payment: %v", paymentErr)
		}
		if i == 0 {
			firstRef = ref
			if _, eventErr := fixturePool.Exec(ctx, `
				INSERT INTO payment_webhook_events (provider, event_id, type, object_ref, payload, unreconciled)
				VALUES ('stripe', $1, 'checkout.session.completed', $2, '{}', 'unsettled_session: pending')`,
				"evt_priority_"+uuid.NewString(), ref); eventErr != nil {
				t.Fatalf("record matching event: %v", eventErr)
			}
		}
	}
	_, uninvoicedID := admintest.PaidPickingOrderForUser(t, fixturePool, admintest.Customer(t, fixturePool), 100000)
	if _, ageErr := fixturePool.Exec(ctx, `
		INSERT INTO order_events (order_id, kind, occurred_at)
		VALUES ($1, 'paid', now() - interval '30 minutes')`, uninvoicedID); ageErr != nil {
		t.Fatalf("age the paid order: %v", ageErr)
	}
	_, claimOrder := admintest.PaidPickingOrderForUser(t, fixturePool, admintest.Customer(t, fixturePool), 100000)
	if _, claimErr := fixturePool.Exec(ctx, `
		INSERT INTO invoice_operations (order_id, kind, provider_key, amount_cents, request_payload,
		    actor_user_id, actor_id_snapshot, actor_kind, request_id, status, last_error)
		VALUES ($1, 'issue', $2, 100000, '{}', $3, $3, 'staff', $2, 'attention', 'issue_lookup_mismatch')`,
		claimOrder, strings.ReplaceAll(uuid.NewString(), "-", "")[:30], actor); claimErr != nil {
		t.Fatalf("record stranded claim: %v", claimErr)
	}
	after, err := store.WorkerHealth(ctx, messages)
	if err != nil {
		t.Fatalf("health after fixtures: %v", err)
	}
	if after.UnreconciledPayments-before.UnreconciledPayments != 2 ||
		after.UninvoicedCount-before.UninvoicedCount != 1 ||
		after.StrandedClaimCount-before.StrandedClaimCount != 1 {
		t.Fatalf("family deltas: payments=%d uninvoiced=%d claims=%d, want 2/1/1",
			after.UnreconciledPayments-before.UnreconciledPayments, after.UninvoicedCount-before.UninvoicedCount,
			after.StrandedClaimCount-before.StrandedClaimCount)
	}
	count, err := store.StaffTaskCount(ctx)
	if err != nil || count-baseline != 4 {
		t.Fatalf("three-family count delta=%d error=%v, want 4", count-baseline, err)
	}
	for range 50 {
		if _, additionalEventErr := fixturePool.Exec(ctx, `
			INSERT INTO payment_webhook_events (provider, event_id, type, object_ref, payload, unreconciled)
			VALUES ('stripe', $1, 'checkout.session.completed', $2, '{}', 'unsettled_session: pending')`,
			"evt_priority_"+uuid.NewString(), firstRef); additionalEventErr != nil {
			t.Fatalf("fill diagnostic sample: %v", additionalEventErr)
		}
	}
	bounded, err := store.WorkerHealth(ctx, messages)
	if err != nil {
		t.Fatalf("health with bounded sample: %v", err)
	}
	if len(bounded.UnreconciledEvents) != 50 {
		t.Fatalf("visible event sample=%d, want 50", len(bounded.UnreconciledEvents))
	}
	count, err = store.StaffTaskCount(ctx)
	if err != nil || count-baseline != 54 {
		t.Fatalf("count beyond sample delta=%d error=%v, want 54", count-baseline, err)
	}
	if count != bounded.StaffTaskCount() {
		t.Errorf("navigation count=%d page count=%d", count, bounded.StaffTaskCount())
	}
	if _, alarmErr := fixturePool.Exec(ctx, `
		INSERT INTO outbox_messages (topic, dedupe_key, payload, attempts, available_at)
		VALUES ('test.priority', $1, '{}', 99, now())`, uuid.NewString()); alarmErr != nil {
		t.Fatalf("add engineering alarm: %v", alarmErr)
	}
	withAlarm, err := store.StaffTaskCount(ctx)
	if err != nil || withAlarm != count {
		t.Errorf("engineering alarm changed staff count: %d to %d error=%v", count, withAlarm, err)
	}
}
