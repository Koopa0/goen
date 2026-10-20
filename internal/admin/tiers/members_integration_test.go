//go:build integration

package tiers_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/db"
)

// TestTheMembershipPageCountsEachCustomerInTheirOwnBand: the page counts every
// account in the band member_tier gives it, from one pass over the spend rather
// than a member_tier call per account (#1934). One customer's only order is
// older than the window, which a sum over the wrong view would count.
func TestTheMembershipPageCountsEachCustomerInTheirOwnBand(t *testing.T) {
	ctx := t.Context()
	for _, cents := range []int64{1000000, 5000000} {
		admintest.OrderForCustomer(t, pool, admintest.CreditedAccount(t, pool, 0), cents, true)
	}
	old := admintest.OrderForCustomer(t, pool, admintest.CreditedAccount(t, pool, 0), 20000000, true)
	if _, err := pool.Exec(ctx, `UPDATE orders SET placed_at = now() - make_interval(days => membership_window_days() + 1)
		WHERE id = $1`, old); err != nil {
		t.Fatalf("age the order past the window: %v", err)
	}

	// One snapshot for the page and the per-account answer, as the back office.
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer db.Rollback(ctx, tx)
	if _, err = tx.Exec(ctx, `SET LOCAL ROLE admin`); err != nil {
		t.Fatalf("assume admin: %v", err)
	}
	page, err := db.New(tx).AdminMembershipTiers(ctx)
	if err != nil {
		t.Fatalf("AdminMembershipTiers: %v", err)
	}
	rows, err := tx.Query(ctx, `SELECT member_tier(u.id, NULL), count(*) FROM users u GROUP BY 1`)
	if err != nil {
		t.Fatalf("read each account's tier: %v", err)
	}
	want, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (struct {
		tier    uuid.NullUUID
		members int64
	}, error) {
		var r struct {
			tier    uuid.NullUUID
			members int64
		}
		return r, row.Scan(&r.tier, &r.members)
	})
	if err != nil {
		t.Fatalf("collect each account's tier: %v", err)
	}
	perTier := map[uuid.UUID]int64{}
	for _, r := range want {
		if r.tier.Valid {
			perTier[r.tier.UUID] = r.members
		}
	}

	banded := int64(0)
	for _, tier := range page {
		if tier.Members != perTier[tier.ID] {
			t.Errorf("the page counts %d in %s, want the %d accounts member_tier places there",
				tier.Members, tier.Code, perTier[tier.ID])
		}
		banded += tier.Members
	}
	if banded == 0 {
		t.Error("the page counts nobody in any band, so it compared nothing")
	}
}
