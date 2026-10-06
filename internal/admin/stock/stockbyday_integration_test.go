//go:build integration

package stock_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/shoptime"
	admin "github.com/koopa0/goen/internal/ui/pages/admin"
)

// moveTo puts the ledger row of key at when, as the owner: the ledger refuses
// every change from anyone else, and a delivery cannot be received forty days
// ago any other way. The stock column is left alone because the row's delta
// does not change.
func moveTo(t *testing.T, key string, when time.Time) {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatalf("disable triggers: %v", err)
	}
	if tag, err := tx.Exec(t.Context(), `UPDATE inventory_movements SET created_at = $2 WHERE idempotency_key = $1`, key, when); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("move %s: %v, %d rows", key, err, tag.RowsAffected())
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// Stock goes back from the column through the ledger, so the fixture writes its
// movements through the one writer of both and only then moves their dates: a
// raw INSERT into the ledger would leave it summing to more than the column and
// every day before it would read too low.
func TestStockByDayIsTheColumnWorkedBackThroughTheLedger(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := stock.NewStore(pool)
	actor := adminID(t)

	var sku string
	if err := pool.QueryRow(ctx, `
		SELECT sku FROM product_variants WHERE is_active ORDER BY position OFFSET 5 LIMIT 1`).Scan(&sku); err != nil {
		t.Fatalf("read variant: %v", err)
	}
	received, adjusted := "line-"+uuid.NewString(), "line-"+uuid.NewString()
	if err := s.Receive(ctx, sku, 30, actor, received); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if err := s.Adjust(ctx, sku, -4, actor, adjusted); err != nil {
		t.Fatalf("adjust: %v", err)
	}
	moveTo(t, received, admintest.ShopNoonDaysAgo(t, 40))
	moveTo(t, adjusted, admintest.ShopNoonDaysAgo(t, 10))

	var ledger, column, today int32
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(m.delta), 0), pv.stock_quantity,
		       coalesce(sum(m.delta) FILTER (WHERE m.created_at >= $2), 0)
		FROM product_variants pv LEFT JOIN inventory_movements m ON m.variant_id = pv.id
		WHERE pv.sku = $1 GROUP BY pv.stock_quantity`, sku, shoptime.Midnight(time.Now())).
		Scan(&ledger, &column, &today); err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if ledger != column {
		t.Fatalf("the fixture's ledger sums to %d but stock_quantity is %d: the days would roll back from the wrong level", ledger, column)
	}

	view, err := s.Movements(ctx, sku)
	if err != nil {
		t.Fatalf("Movements: %v", err)
	}
	days := view.Days
	if len(days) != admin.StockLineDays {
		t.Fatalf("Movements gave %d days, want %d", len(days), admin.StockLineDays)
	}
	last := len(days) - 1
	if days[last].Stock != column {
		t.Errorf("the last day closes at %d, want stock_quantity %d", days[last].Stock, column)
	}

	// Seed and earlier tests only moved stock today, so before today it was the
	// column less what moved today.
	before := column - today
	for _, tc := range []struct {
		name              string
		daysAgo           int
		stock, rec, moves int32
	}{
		{"yesterday", 1, before, 0, 0},
		{"the day stock was adjusted down", 10, before, 0, 1},
		{"the day before it", 11, before + 4, 0, 0},
		{"the day goods were received", 40, before + 4, 30, 1},
		{"the day before that", 41, before + 4 - 30, 0, 0},
	} {
		d := days[last-tc.daysAgo]
		if d.Stock != tc.stock || d.Received != tc.rec || d.Moves != tc.moves {
			t.Errorf("%s: stock %d, received %d, moves %d, want %d, %d, %d",
				tc.name, d.Stock, d.Received, d.Moves, tc.stock, tc.rec, tc.moves)
		}
	}
	if got := days[last-40].Receipts; got != 1 {
		t.Errorf("the day goods were received counts %d receipts, want 1", got)
	}
}
