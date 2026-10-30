//go:build integration

package checkout_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestHandler_PlaceOrderApplyBlankForm: Applying a wrong code on a blank form
// refuses only the code: the coupon field is marked and says why, and no alert
// sends the shopper through the fields. The shopper pressed 套用 before typing
// an address, so nothing but the code may be refused.
func TestHandler_PlaceOrderApplyBlankForm(t *testing.T) {
	g := newCouponGuesses(t, "coupon-blank")
	form := url.Values{"coupon": {"NOPE123"}, "update": {"coupon"}, "shipping": {g.ship.String()}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.9:5000"
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: g.guestCart()}) //nolint:gosec // G124: dev cart cookie under test
	res := httptest.NewRecorder()
	g.h.PlaceOrder(res, req)

	body := res.Body.String()
	if res.Code != http.StatusOK {
		t.Fatalf("applying a code answered %d, want 200", res.Code)
	}
	unknown := i18n.T(t.Context(), i18n.KeyCouponUnknown)
	if !strings.Contains(body, `id="coupon-error"`) || !strings.Contains(body, unknown) {
		t.Error("the refused code is not shown beside the coupon field")
	}
	if strings.Contains(body, `id="checkout-refusal"`) {
		t.Error("an alert sends the shopper through the fields for a refused code")
	}
	if n := strings.Count(body, unknown); n != 1 {
		t.Errorf("the refused code's sentence is on the page %d times, want once", n)
	}
	if n := strings.Count(body, `aria-invalid="true"`); n != 1 {
		t.Errorf("%d controls are marked refused, want the coupon field alone", n)
	}
}
