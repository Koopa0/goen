//go:build integration

package policy

import (
	"testing"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/db/dbtest"
)

// TestStore_RulesSchemaWindows: the windows the shop rules state are the
// schema's. The windows the stat line states are the schema's, read as the
// store role the storefront's pages read through.
func TestStore_RulesSchemaWindows(t *testing.T) {
	ctx := t.Context()
	owner := dbtest.Pool(t)
	var want Rules
	if err := owner.QueryRow(ctx, `
		SELECT rescission_days(), return_goodwill_days(), checkout_hold_minutes(), checkout_pay_start_minutes()`).
		Scan(&want.RescissionDays, &want.ReturnDays, &want.HoldMinutes, &want.PayStartMinutes); err != nil {
		t.Fatalf("read the windows: %v", err)
	}
	tx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer db.Rollback(ctx, tx)
	if _, err = tx.Exec(ctx, `SET LOCAL ROLE store`); err != nil {
		t.Fatalf("assume store: %v", err)
	}
	got, err := NewStore(tx).Rules(ctx)
	if err != nil {
		t.Fatalf("read the shop rules as store: %v", err)
	}
	if got.RescissionDays != want.RescissionDays || got.ReturnDays != want.ReturnDays ||
		got.HoldMinutes != want.HoldMinutes || got.PayStartMinutes != want.PayStartMinutes {
		t.Errorf("Rules states %d/%d days and %d/%d minutes, the schema names %d/%d and %d/%d",
			got.RescissionDays, got.ReturnDays, got.HoldMinutes, got.PayStartMinutes,
			want.RescissionDays, want.ReturnDays, want.HoldMinutes, want.PayStartMinutes)
	}
}
