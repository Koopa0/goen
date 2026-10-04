//go:build integration

package stock_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/admin/stock"
)

// Two variants of one product differ only by an option value, so the list must
// carry it, and a search by the SKU fragment or the product name must find
// exactly them, wildcards in the term being plain characters.
func TestTheStockListSearchesBySKUOrNameAndShowsOptionValues(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	slug := admintest.DraftProduct(t, ctx, pool, s)
	if errs, err := s.AddOption(ctx, slug, products.OptionDraft{Name: "顏色"}); err != nil || len(errs) > 0 {
		t.Fatalf("AddOption: %v %v", err, errs)
	}
	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"星霧藍", "曜石黑"} {
		if errs, addErr := s.AddOptionValue(ctx, slug, products.OptionDraft{OptionID: view.Options[0].ID, Name: v}); addErr != nil || len(errs) > 0 {
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
		if errs, addErr := s.AddVariant(ctx, slug, &products.VariantForm{SKU: sku, PriceCents: 100000, OptionValues: []string{val.ID}}); addErr != nil || len(errs) > 0 {
			t.Fatalf("AddVariant %s: %v %v", sku, addErr, errs)
		}
	}

	for _, term := range []string{strings.ToLower(stem), slug} {
		got, readErr := stock.NewStore(pool).Variants(ctx, false, term)
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
		got, readErr := stock.NewStore(pool).Variants(ctx, false, term)
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
