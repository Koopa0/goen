//go:build integration

package cart_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/orderaccess"
)

var checkedShipping = regexp.MustCompile(`name="shipping" value="([^"]*)" checked`)

// TestAGuestPlacesTheOrderTheCheckoutRendered follows a first guest checkout
// as the browser does: the page carries its quote, an attempt ID and a shipping
// method already chosen; posting exactly those places the order, redirects to
// its payment page and hands this browser the cookie that opens it.
func TestAGuestPlacesTheOrderTheCheckoutRendered(t *testing.T) {
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler), false, testLimiter(), nil, nil)
	cartID, token := newCartSession(t, s)
	if err := s.Add(t.Context(), cartID, freshVariant(t, "rendered"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	page, status := openTheCheckout(t, h, token, "")
	if status != http.StatusOK {
		t.Fatalf("GET /checkout answered %d, want 200", status)
	}
	quote, hasQuote := hiddenInputValue(page, "checkout_quote")
	attempt, hasAttempt := hiddenInputValue(page, "idempotency")
	chosen := checkedShipping.FindStringSubmatch(page)
	if !hasQuote || quote == "" || !hasAttempt || attempt == "" || chosen == nil {
		t.Fatalf("GET /checkout rendered quote %q (found %v), attempt %q (found %v), "+
			"checked shipping %v; want all three", quote, hasQuote, attempt, hasAttempt, chosen)
	}

	email := "rendered-" + uuid.NewString() + "@example.com"
	form := url.Values{
		"email": {email}, "name": {"王小明"}, "phone": {"0912345678"},
		"postal_code": {"110"}, "city": {"台北市"}, "district": {"信義區"},
		"street":         {"松高路 1 號"},
		"shipping":       {chosen[1]},
		"checkout_quote": {quote},
		"idempotency":    {attempt},
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: the browser's own cart cookie
	res := httptest.NewRecorder()
	h.PlaceOrder(res, req)

	if res.Code != http.StatusSeeOther {
		t.Fatalf("POST /checkout with the rendered quote answered %d, want 303; body=%s",
			res.Code, res.Body.String())
	}
	location := res.Header().Get("Location")
	number, ok := strings.CutSuffix(strings.TrimPrefix(location, "/orders/"), "/pay")
	if !ok || number == "" || number == location {
		t.Fatalf("POST /checkout redirected to %q, want /orders/{number}/pay", location)
	}
	var placed *http.Cookie
	for _, c := range res.Result().Cookies() {
		if c.Name == "goen_placed" {
			placed = c
		}
	}
	if placed == nil {
		t.Fatal("POST /checkout set no goen_placed cookie")
	}
	followPayWithCookie(t, t.Context(), testPayHandler(t), number, placed)
}
