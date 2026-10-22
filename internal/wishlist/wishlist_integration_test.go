//go:build integration

package wishlist_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/wishlist"
)

// TestStore_ItemsSoleVariant: "add to cart" beside a product with a choice of
// variants would pick one for the customer, so only a product with a single
// variant, in stock, names one.
func TestStore_ItemsSoleVariant(t *testing.T) {
	ctx := t.Context()
	s := wishlist.NewStore(pool)
	u := register(t, "wish-"+uuid.NewString()+"@example.com")

	variant := ownVariant(t, "wish")
	var slug string
	var productID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT p.slug, p.id FROM products p JOIN product_variants v ON v.product_id = p.id WHERE v.id = $1`,
		variant).Scan(&slug, &productID); err != nil {
		t.Fatalf("read the fixture product: %v", err)
	}
	if err := s.Save(ctx, u.ID, slug); err != nil {
		t.Fatalf("Save: %v", err)
	}

	soleVariant := func() string {
		t.Helper()
		tiles, err := s.Items(ctx, u.ID)
		if err != nil {
			t.Fatalf("Items: %v", err)
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

// TestHandler_UpdateSave drives POST /account/wishlist as a signed-in customer:
// the product is saved and the browser goes back to the page named in return.
func TestHandler_UpdateSave(t *testing.T) {
	ctx := t.Context()
	s := wishlist.NewStore(pool)
	u := register(t, "wish-post-"+uuid.NewString()+"@example.com")
	variant := ownVariant(t, "wishpost")
	var slug string
	if err := pool.QueryRow(ctx,
		`SELECT p.slug FROM products p JOIN product_variants v ON v.product_id = p.id WHERE v.id = $1`,
		variant).Scan(&slug); err != nil {
		t.Fatalf("read the fixture product: %v", err)
	}
	h := wishlist.NewHandler(s, slog.New(slog.DiscardHandler))

	form := url.Values{"slug": {slug}, "return": {"/p/" + slug}}
	req := httptest.NewRequestWithContext(user.NewContext(ctx, u), http.MethodPost,
		"/account/wishlist", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("Update answered %d, want 303; body=%s", w.Code, w.Body.String())
	}
	if got, want := w.Header().Get("Location"), "/p/"+slug; got != want {
		t.Errorf("Update redirects to %q, want %q", got, want)
	}
	tiles, err := s.Items(ctx, u.ID)
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	if len(tiles) != 1 {
		t.Errorf("the wishlist holds %d products after saving one, want 1", len(tiles))
	}
}
