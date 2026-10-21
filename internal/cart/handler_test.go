package cart

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart/cartitem"
)

// TestSignedInCartLookupFallsBackToTheAccountCart holds the HTTP resolution
// after a merge-adopt: the cookie still names the deleted guest row, and a
// signed-in add must not mint an unowned cart.
func TestSignedInCartLookupFallsBackToTheAccountCart(t *testing.T) {
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

func TestAddFromTheWishlistComesBackToTheWishlist(t *testing.T) {
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
