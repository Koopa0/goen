//go:build integration

package admin_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
)

func stockOfSKU(t *testing.T, sku string) int32 {
	t.Helper()
	var n int32
	if err := pool.QueryRow(t.Context(),
		`SELECT stock_quantity FROM product_variants WHERE sku = $1`, sku).Scan(&n); err != nil {
		t.Fatalf("read stock of %s: %v", sku, err)
	}
	return n
}

func formKeyOf(t *testing.T, s *admin.Store, sku string) string {
	t.Helper()
	view, err := s.Variants(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	for i := range view.Variants {
		if view.Variants[i].SKU == sku {
			return view.Variants[i].AdjustKey()
		}
	}
	t.Skipf("%s is not on the first page of the stock list", sku)
	return ""
}

// Stock returning to a level it was at before must not spend the next form's
// key: each rendered form carries its own.
func TestAnAdjustmentIsAcceptedWhenStockReturnsToAnEarlierLevel(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	actor := staffID(t)

	var sku string
	if err := pool.QueryRow(ctx, `
		SELECT sku FROM product_variants WHERE is_active ORDER BY position LIMIT 1`).Scan(&sku); err != nil {
		t.Fatal(err)
	}
	start := stockOfSKU(t, sku)
	for i, delta := range []int32{1, -1, 1} {
		if err := s.AdjustStock(ctx, sku, delta, actor, formKeyOf(t, s, sku)); err != nil {
			t.Fatalf("adjustment %d (%+d) at stock %d: %v", i+1, delta, stockOfSKU(t, sku), err)
		}
	}
	if got := stockOfSKU(t, sku); got != start+1 {
		t.Errorf("stock = %d, want %d", got, start+1)
	}
}

// The same for a goods receipt, and a replay of an applied receipt is the
// earlier success, not a second delivery and not a refusal.
func TestAGoodsReceiptSurvivesStockReturningAndAReplayIsOneDelivery(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	actor := staffID(t)

	var sku string
	if err := pool.QueryRow(ctx, `
		SELECT sku FROM product_variants WHERE is_active ORDER BY position LIMIT 1`).Scan(&sku); err != nil {
		t.Fatal(err)
	}
	keyOf := func() string {
		view, err := s.Movements(ctx, sku)
		if err != nil {
			t.Fatal(err)
		}
		return view.ReceiveKey()
	}
	start := stockOfSKU(t, sku)

	first := keyOf()
	if err := s.ReceiveStock(ctx, sku, 3, actor, first); err != nil {
		t.Fatalf("first receipt: %v", err)
	}
	if err := s.AdjustStock(ctx, sku, -3, actor, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := s.ReceiveStock(ctx, sku, 3, actor, keyOf()); err != nil {
		t.Fatalf("receipt after stock returned to %d: %v", start, err)
	}
	if got := stockOfSKU(t, sku); got != start+3 {
		t.Fatalf("stock = %d, want %d", got, start+3)
	}

	if err := s.ReceiveStock(ctx, sku, 3, actor, first); err != nil {
		t.Errorf("replaying an applied receipt = %v, want the earlier success", err)
	}
	if got := stockOfSKU(t, sku); got != start+3 {
		t.Errorf("the replay moved stock: %d, want %d", got, start+3)
	}
	if err := s.ReceiveStock(ctx, sku, 4, actor, first); !errors.Is(err, admin.ErrRefused) {
		t.Errorf("reusing the key for a different receipt = %v, want ErrRefused", err)
	}
}
