//go:build integration

package cart_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/picture"
)

// TestStore_ViewCurrentPrice holds that View shows a line's current price and
// availability.
func TestStore_ViewCurrentPrice(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	vid := freshVariant(t, "current-price")

	if err := s.Add(ctx, id, vid, 2); err != nil {
		t.Fatalf("add: %v", err)
	}
	view, err := s.View(ctx, id)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if len(view.Lines) != 1 {
		t.Fatalf("cart holds %d lines, want 1", len(view.Lines))
	}
	if view.Lines[0].Unavailable || view.Lines[0].Short {
		t.Fatal("a freshly added, in-stock line reads as unavailable")
	}
	if view.ItemCount != 2 {
		t.Errorf("item count = %d, want 2 — the badge counts units, not lines", view.ItemCount)
	}
	want := view.Lines[0].UnitCents * 2
	if view.SubtotalCents != want {
		t.Errorf("subtotal = %d, want %d", view.SubtotalCents, want)
	}
}

// TestStore_ViewColourPhoto holds the cart thumbnail to the line's variant: its
// value's photograph when the shop tagged one, else the product's first, which
// is the only picture an untagged product has.
func TestStore_ViewColourPhoto(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	for _, sku := range []string{"PXL-9P-2-1", "PXL-9P-1-1"} {
		var variant uuid.UUID
		if err := pool.QueryRow(ctx,
			`SELECT id FROM product_variants WHERE sku = $1`, sku).Scan(&variant); err != nil {
			t.Fatalf("read %s: %v", sku, err)
		}
		if err := s.Add(ctx, id, variant, 1); err != nil {
			t.Fatalf("add %s: %v", sku, err)
		}
	}
	view, err := s.View(ctx, id)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if len(view.Lines) != 2 {
		t.Fatalf("cart holds %d lines, want 2", len(view.Lines))
	}
	want := map[string]string{
		"PXL-9P-2-1": picture.Image{BundledKey: "pixelight-9-pro-02.webp"}.URL(), // 曜石黑, tagged by the seed
		"PXL-9P-1-1": picture.Image{BundledKey: "pixelight-9-pro-01.webp"}.URL(), // 星霧藍, the product's first
	}
	for _, line := range view.Lines {
		if line.ImageURL != want[line.SKU] {
			t.Errorf("line %s shows %q, want %q", line.SKU, line.ImageURL, want[line.SKU])
		}
	}
}

// TestStore_ItemCountBadge holds that ItemCount counts what the cart holds for
// the header badge.
func TestStore_ItemCountBadge(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	a, b, _ := threeVariants(t, "badgefix")

	count := func() int {
		t.Helper()
		n, err := s.ItemCount(ctx, id)
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	if got := count(); got != 0 {
		t.Errorf("an empty cart counts %d, want 0", got)
	}
	if err := s.Add(ctx, id, a, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	if got := count(); got != 1 {
		t.Errorf("one unit counts %d, want 1", got)
	}
	if err := s.Add(ctx, id, a, 2); err != nil {
		t.Fatalf("add: %v", err)
	}
	if got := count(); got != 3 {
		t.Errorf("three units of one variant count %d, want 3 — the badge is "+
			"counting lines, not items", got)
	}
	if err := s.Add(ctx, id, b, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	if got := count(); got != 4 {
		t.Errorf("four units across two variants count %d, want 4", got)
	}

	other := newCart(t, s)
	if err := s.Add(ctx, other, a, 5); err != nil {
		t.Fatalf("add to other: %v", err)
	}
	if got := count(); got != 4 {
		t.Errorf("this cart counts %d after another cart was filled, want 4", got)
	}
}
