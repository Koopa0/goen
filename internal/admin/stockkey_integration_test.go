//go:build integration

package admin_test

import (
	"errors"
	"fmt"
	"strings"
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
	view, err := s.Variants(t.Context(), false, "")
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

// Two variants of one product differ only by an option value, so the list must
// carry it, and a search by the SKU fragment or the product name must find
// exactly them, wildcards in the term being plain characters.
func TestTheStockListSearchesBySKUOrNameAndShowsOptionValues(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	slug := draftProduct(t, ctx, s)
	if errs, err := s.AddOption(ctx, slug, admin.OptionDraft{Name: "顏色"}); err != nil || len(errs) > 0 {
		t.Fatalf("AddOption: %v %v", err, errs)
	}
	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"星霧藍", "曜石黑"} {
		if errs, addErr := s.AddOptionValue(ctx, slug, admin.OptionDraft{OptionID: view.Options[0].ID, Name: v}); addErr != nil || len(errs) > 0 {
			t.Fatalf("AddOptionValue: %v %v", addErr, errs)
		}
	}
	view, err = s.Product(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	stem := "SRCH-" + strings.ToUpper(uuid.NewString()[:8])
	for i, val := range view.Options[0].Values {
		sku := fmt.Sprintf("%s-%d", stem, i+1)
		if errs, addErr := s.AddVariant(ctx, slug, &admin.VariantForm{SKU: sku, PriceCents: 100000, OptionValues: []string{val.ID}}); addErr != nil || len(errs) > 0 {
			t.Fatalf("AddVariant %s: %v %v", sku, addErr, errs)
		}
	}

	for _, term := range []string{strings.ToLower(stem), slug} {
		got, readErr := s.Variants(ctx, false, term)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if len(got.Variants) != 2 {
			t.Fatalf("search %q found %d variants, want 2", term, len(got.Variants))
		}
		for i, want := range []string{"星霧藍", "曜石黑"} {
			if got.Variants[i].OptionText() != want {
				t.Errorf("search %q row %d options = %q, want %q", term, i, got.Variants[i].OptionText(), want)
			}
		}
	}
	for _, term := range []string{"%", "_", stem + "-9"} {
		got, readErr := s.Variants(ctx, false, term)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for i := range got.Variants {
			if !strings.Contains(strings.ToLower(got.Variants[i].SKU+got.Variants[i].ProductName), strings.ToLower(term)) {
				t.Errorf("search %q returned %s %q", term, got.Variants[i].SKU, got.Variants[i].ProductName)
			}
		}
	}
}
