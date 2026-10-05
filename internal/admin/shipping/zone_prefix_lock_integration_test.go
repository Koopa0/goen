//go:build integration

package shipping_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/shipping"
	"github.com/koopa0/goen/internal/db"
)

func TestZonesTradingPrefixesSaveOneAfterTheOther(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := shipping.NewStore(admintest.AdminRolePool(t, pool))
	p := unusedZonePrefixes(t, 2)
	zoneA := createZoneWithPrefixes(t, ctx, s, "甲區", p[0:1])
	zoneB := createZoneWithPrefixes(t, ctx, s, "乙區", p[1:2])

	both := p[0] + " " + p[1]
	reversed := p[1] + " " + p[0]
	saveWhileAssignmentsAreHeld(t, ctx,
		func() error { _, err := s.SetZonePrefixes(ctx, zoneA.String(), both); return err },
		func() error { _, err := s.SetZonePrefixes(ctx, zoneB.String(), reversed); return err },
	)

	var ownerOfFirst, ownerOfSecond uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT zone_id FROM shipping_zone_prefixes WHERE prefix = $1`, p[0]).
		Scan(&ownerOfFirst); err != nil {
		t.Fatalf("owner of %s: %v", p[0], err)
	}
	if err := pool.QueryRow(ctx, `SELECT zone_id FROM shipping_zone_prefixes WHERE prefix = $1`, p[1]).
		Scan(&ownerOfSecond); err != nil {
		t.Fatalf("owner of %s: %v", p[1], err)
	}
	if ownerOfFirst != ownerOfSecond || (ownerOfFirst != zoneA && ownerOfFirst != zoneB) {
		t.Errorf("prefixes %s and %s belong to %s and %s, want both to one of %s or %s",
			p[0], p[1], ownerOfFirst, ownerOfSecond, zoneA, zoneB)
	}
}

func TestANewZoneWaitsForThePrefixAssignments(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := shipping.NewStore(admintest.AdminRolePool(t, pool))
	p := unusedZonePrefixes(t, 1)
	saveWhileAssignmentsAreHeld(t, ctx, func() error {
		_, err := s.CreateZone(ctx, &shipping.NewZone{
			Code: "zone_" + uuid.NewString()[:8], Name: "丙區", Prefixes: p[0],
		})
		return err
	})
}

// saveWhileAssignmentsAreHeld starts the writers while another transaction
// holds the prefix-assignment lock, waits until every one is queued on it, then
// releases it. A writer that reaches a prefix row without the lock never queues.
func saveWhileAssignmentsAreHeld(t *testing.T, ctx context.Context, writers ...func() error) {
	t.Helper()
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	defer func() { _ = holder.Rollback(context.WithoutCancel(ctx)) }()
	if lockErr := db.New(holder).LockZonePrefixAssignments(ctx); lockErr != nil {
		t.Fatalf("hold the prefix-assignment lock: %v", lockErr)
	}

	results := make(chan error, len(writers))
	var wg sync.WaitGroup
	for _, write := range writers {
		wg.Go(func() { results <- write() })
	}
	waitForAdvisoryWaiters(t, ctx, len(writers))
	if err := holder.Commit(ctx); err != nil {
		t.Fatalf("release the prefix-assignment lock: %v", err)
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("a zone save failed: %v", err)
		}
	}
}

func waitForAdvisoryWaiters(t *testing.T, ctx context.Context, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`).
			Scan(&waiting); err != nil {
			t.Fatalf("count advisory waiters: %v", err)
		}
		if waiting >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d zone saves wait on the prefix-assignment lock, want %d: they touch prefix rows without holding it", waiting, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
