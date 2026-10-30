//go:build integration

package cart_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/ordertest"
	"github.com/koopa0/goen/internal/user"
)

// TestHandler_PageEmptyWishlist holds that an empty cart offers the wishlist to
// a signed-in customer whose wishlist has products, with or without a cart row,
// and to nobody else, and that a cart with items does not even ask.
func TestHandler_PageEmptyWishlist(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"empty-cart-"+uuid.NewString()+"@example.com").Scan(&userID); err != nil {
		t.Fatalf("create the customer: %v", err)
	}
	basket, token := newCartSession(t, s)
	h := cart.NewHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil)
	hasSaved, asked := true, 0
	h.OfferWishlist(func(context.Context, string) (bool, error) {
		asked++
		return hasSaved, nil
	})

	offered := func(signedIn bool, cookies ...*http.Cookie) bool {
		t.Helper()
		reqCtx := ctx
		if signedIn {
			reqCtx = user.NewContext(ctx, user.User{ID: userID.String()})
		}
		r := httptest.NewRequestWithContext(reqCtx, http.MethodGet, "/cart", http.NoBody)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.Page(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("cart page answered %d", w.Code)
		}
		return strings.Contains(w.Body.String(), i18n.T(ctx, i18n.KeyCartEmptyWishlist))
	}
	guestCart := &http.Cookie{Name: "goen_cart", Value: token} //nolint:gosec // G124: fixture browser cart cookie.

	if !offered(true) {
		t.Error("a signed-in customer with no cart row is not offered the wishlist")
	}
	if !offered(true, guestCart) {
		t.Error("a signed-in customer with an empty cart is not offered the wishlist")
	}
	if offered(false, guestCart) {
		t.Error("a guest is offered the wishlist")
	}
	hasSaved = false
	if offered(true) {
		t.Error("a customer with nothing saved is offered the wishlist")
	}

	hasSaved, asked = true, 0
	if err := s.Add(ctx, basket, ordertest.FreshVariant(t, pool, "wishlist-line"), 1); err != nil {
		t.Fatalf("fill the cart: %v", err)
	}
	if offered(true, guestCart) || asked != 0 {
		t.Errorf("a cart with items asked the wishlist %d times, want none", asked)
	}
}
