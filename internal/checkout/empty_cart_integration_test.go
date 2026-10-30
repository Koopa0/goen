//go:build integration

package checkout_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/checkout/checkouttest"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// TestHandler_PageEmptyCart: Page sends a visitor with nothing in the cart, whether the
// cart row was emptied by an order or never existed, to the cart page with the reason
// checkout cannot continue, and sends a cart blocked by a sold-out line to the bare cart
// page, which already names that line.
func TestHandler_PageEmptyCart(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	h := newHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), nil, nil)

	sentTo := func(cookie string) string {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/checkout", http.NoBody)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "goen_cart", Value: cookie}) //nolint:gosec // G124: the browser's own cart cookie
		}
		res := httptest.NewRecorder()
		h.Page(res, req)
		if res.Code != http.StatusSeeOther {
			t.Fatalf("GET /checkout answered %d, want 303 back to the cart", res.Code)
		}
		return res.Header().Get("Location")
	}

	if got := sentTo(""); got != cart.CheckoutEmptyPath {
		t.Errorf("GET /checkout with no cart went to %q, want %q", got, cart.CheckoutEmptyPath)
	}

	cartID, token := checkouttest.NewCartSession(t, s)
	if got := sentTo(token); got != cart.CheckoutEmptyPath {
		t.Errorf("GET /checkout with an empty cart went to %q, want %q", got, cart.CheckoutEmptyPath)
	}

	gone := ordertest.FreshVariant(t, pool, "empty-cart-blocked")
	if err := s.Carts().Add(ctx, cartID, gone, 1); err != nil {
		t.Fatalf("add the line that sells out: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET stock_quantity = safety_stock WHERE id = $1`, gone); err != nil {
		t.Fatalf("sell out: %v", err)
	}
	if got := sentTo(token); got != "/cart" {
		t.Errorf("GET /checkout with a sold-out line went to %q, want the bare cart page", got)
	}
}
