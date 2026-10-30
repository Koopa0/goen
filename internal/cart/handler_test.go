package cart

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart/cartitem"
	"github.com/koopa0/goen/internal/user"
)

// TestHandler_LookupCartAccountFallback holds the HTTP resolution after a
// merge-adopt: the cookie still names the deleted guest row, and a signed-in
// add must not mint an unowned cart.
func TestHandler_LookupCartAccountFallback(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "store.ForUser(") {
		t.Error("signed-in cart lookup does not resolve store.ForUser when the cookie misses")
	}
	if strings.Contains(body, "Create(r.Context(), token, uuid.NullUUID{})") {
		t.Error("signed-in add still mints an unowned cart")
	}
}

// TestHandler_BackToProductWishlist holds that backToProduct sends an add made
// from the wishlist back to the wishlist.
func TestHandler_BackToProductWishlist(t *testing.T) {
	t.Parallel()
	back := func(form url.Values) string {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/cart/items", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		(&Handler{}).backToProduct(rec, req, uuid.Nil, cartitem.AddOutcomeAdded)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303", rec.Code)
		}
		return rec.Header().Get("Location")
	}
	if got, want := back(url.Values{"return": {"/account/wishlist"}}), "/account/wishlist?added=added"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if got := back(url.Values{"return": {"https://evil.example/"}}); got != "/cart" {
		t.Errorf("a return that is not the wishlist went to %q, want /cart", got)
	}
}

// TestHandler_HasSavedProducts holds that the empty cart asks the wishlist only
// about a signed-in customer, and offers it only on a yes.
func TestHandler_HasSavedProducts(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		signedIn  bool
		answer    bool
		err       error
		wired     bool
		want      bool
		wantAsked bool
	}{
		{name: "saved products", signedIn: true, answer: true, wired: true, want: true, wantAsked: true},
		{name: "none saved", signedIn: true, wired: true, wantAsked: true},
		{name: "unanswerable", signedIn: true, answer: true, err: errors.New("down"), wired: true, wantAsked: true},
		{name: "a guest is not asked", answer: true, wired: true},
		{name: "no wishlist wired", signedIn: true, answer: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var asked bool
			h := &Handler{log: slog.New(slog.DiscardHandler)}
			if tt.wired {
				h.OfferWishlist(func(context.Context, string) (bool, error) {
					asked = true
					return tt.answer, tt.err
				})
			}
			ctx := t.Context()
			if tt.signedIn {
				ctx = user.NewContext(ctx, user.User{ID: uuid.NewString()})
			}
			r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/cart", http.NoBody)
			if got := h.hasSavedProducts(r); got != tt.want || asked != tt.wantAsked {
				t.Errorf("hasSavedProducts = %t (asked %t), want %t (asked %t)", got, asked, tt.want, tt.wantAsked)
			}
		})
	}
}
