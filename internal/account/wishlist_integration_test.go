//go:build integration

package account_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// TestAMemberSavesAProductToTheWishlistAndReturnsWhereTheyWere drives POST
// /account/wishlist as a signed-in customer: the product is saved and the
// browser goes back to the page named in return.
func TestAMemberSavesAProductToTheWishlistAndReturnsWhereTheyWere(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "wish-post-"+uuid.NewString()+"@example.com")
	variant := ownVariant(t, "wishpost")
	var slug string
	if err := pool.QueryRow(ctx,
		`SELECT p.slug FROM products p JOIN product_variants v ON v.product_id = p.id WHERE v.id = $1`,
		variant).Scan(&slug); err != nil {
		t.Fatalf("read the fixture product: %v", err)
	}
	h := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)

	form := url.Values{"slug": {slug}, "return": {"/p/" + slug}}
	req := httptest.NewRequestWithContext(account.WithUser(ctx, u), http.MethodPost,
		"/account/wishlist", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.SaveWishlist(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("SaveWishlist answered %d, want 303; body=%s", w.Code, w.Body.String())
	}
	if got, want := w.Header().Get("Location"), "/p/"+slug; got != want {
		t.Errorf("SaveWishlist redirects to %q, want %q", got, want)
	}
	tiles, err := s.Wishlist(ctx, u.ID)
	if err != nil {
		t.Fatalf("Wishlist: %v", err)
	}
	if len(tiles) != 1 {
		t.Errorf("the wishlist holds %d products after saving one, want 1", len(tiles))
	}
}
