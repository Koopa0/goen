//go:build integration

package account_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
)

// TestAWishlistRowOffersTheCartOnlyForAProductWithOneVariant: "add to cart"
// beside a product with a choice of variants would pick one for the customer,
// so only a product with a single variant, in stock, names one.
func TestAWishlistRowOffersTheCartOnlyForAProductWithOneVariant(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "wish-"+uuid.NewString()+"@example.com")

	variant := ownVariant(t, "wish")
	var slug string
	var productID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT p.slug, p.id FROM products p JOIN product_variants v ON v.product_id = p.id WHERE v.id = $1`,
		variant).Scan(&slug, &productID); err != nil {
		t.Fatalf("read the fixture product: %v", err)
	}
	if err := s.SaveToWishlist(ctx, u.ID, slug); err != nil {
		t.Fatalf("SaveToWishlist: %v", err)
	}

	soleVariant := func() string {
		t.Helper()
		tiles, err := s.Wishlist(ctx, u.ID)
		if err != nil {
			t.Fatalf("Wishlist: %v", err)
		}
		if len(tiles) != 1 {
			t.Fatalf("wishlist has %d products, want 1", len(tiles))
		}
		return tiles[0].SoleVariantID
	}

	if got := soleVariant(); got != variant.String() {
		t.Errorf("a product with one variant in stock names %q, want %q", got, variant)
	}

	var second uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO product_variants (product_id, sku, price_cents, is_active, position)
		VALUES ($1, $2, 199900, true, 1) RETURNING id`,
		productID, "WISH2-"+strings.ToUpper(uuid.NewString()[:8])).Scan(&second); err != nil {
		t.Fatalf("add a second variant: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`SELECT record_inventory_movement($1, 10, 'adjustment', $2, 'admin', NULL, NULL)`,
		second, "fixture:"+second.String()); err != nil {
		t.Fatalf("stock the second variant: %v", err)
	}
	if got := soleVariant(); got != "" {
		t.Errorf("a product with two variants names %q, want none", got)
	}
}
