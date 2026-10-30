//go:build integration

package checkout_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/checkout/checkouttest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// TestHandler_PlaceOrderRefusalSummary: PlaceOrder answers a form refused for a field
// and for an unknown code with one alert that counts and links the field alone, and
// says the code's sentence once, under the code's field.
func TestHandler_PlaceOrderRefusalSummary(t *testing.T) {
	s := checkout.NewStore(pool)
	h := couponTestHandler(s)
	cartID, token := checkouttest.NewCartSession(t, s)
	if err := s.Carts().Add(t.Context(), cartID, ordertest.FreshVariant(t, pool, "refusal-summary"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	page, status := openTheCheckout(t, h, token, "")
	if status != http.StatusOK {
		t.Fatalf("GET /checkout answered %d, want 200", status)
	}
	quote, _ := hiddenInputValue(page, "checkout_quote")
	attempt, _ := hiddenInputValue(page, "idempotency")
	chosen := checkedShipping.FindStringSubmatch(page)
	if chosen == nil {
		t.Fatal("GET /checkout chose no delivery method")
	}

	res := checkoutPost(t, h, token, url.Values{
		"email": {"summary@example.com"}, "name": {"王小明"}, "phone": {""},
		"postal_code": {"110"}, "city": {"台北市"}, "district": {"信義區"}, "street": {"松高路 1 號"},
		"coupon": {"NOPE123"}, "shipping": {chosen[1]}, "checkout_quote": {quote}, "idempotency": {attempt},
	})
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /checkout answered %d, want 422", res.Code)
	}
	body := res.Body.String()
	if n := strings.Count(body, `id="checkout-refusal"`); n != 1 {
		t.Fatalf("%d refusal alerts, want one", n)
	}
	for _, want := range []string{"請修正 1 個欄位", `href="#phone"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal does not carry %q", want)
		}
	}
	if strings.Contains(body, `href="#coupon"`) {
		t.Error("the alert lists the refused code")
	}
	if unknown := i18n.T(t.Context(), i18n.KeyCouponUnknown); strings.Count(body, unknown) != 1 {
		t.Errorf("the code's sentence is on the page %d times, want once", strings.Count(body, unknown))
	}
}

// refusedCode is a discount code no coupon carries.
const refusedCode = "NOPE123"

var (
	autofocused = regexp.MustCompile(`\sautofocus[\s/>=]`)
	codeField   = regexp.MustCompile(`<input[^>]*\sid="coupon"[^>]*>`)
)

// TestHandler_PlaceOrderRefusalFocus: a code the shop does not know takes focus to its field
// only on a page that opens whole, from the submit it refused alone or from 套用 without
// scripting. An answer htmx swaps into the page showing, from a chooser or from 套用, asks for
// focus nowhere: htmx would move it from the control the shopper just changed, and with it the
// scroll position.
func TestHandler_PlaceOrderRefusalFocus(t *testing.T) {
	for _, tt := range []struct {
		name    string
		update  string
		htmx    bool
		status  int
		focused bool
	}{
		{"submit refused for the code", "", false, http.StatusUnprocessableEntity, true},
		{"apply without scripting", "coupon", false, http.StatusOK, true},
		{"apply with scripting", "coupon", true, http.StatusOK, false},
		{"delivery method with scripting", "shipping", true, http.StatusOK, false},
		{"saved address with scripting", "address", true, http.StatusOK, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := checkout.NewStore(pool)
			h := couponTestHandler(s)
			cartID, token := checkouttest.NewCartSession(t, s)
			if err := s.Carts().Add(t.Context(), cartID, ordertest.FreshVariant(t, pool, "refusal-focus"), 1); err != nil {
				t.Fatalf("add: %v", err)
			}
			page, status := openTheCheckout(t, h, token, "")
			if status != http.StatusOK {
				t.Fatalf("GET /checkout answered %d, want 200", status)
			}
			quote, _ := hiddenInputValue(page, "checkout_quote")
			attempt, _ := hiddenInputValue(page, "idempotency")
			chosen := checkedShipping.FindStringSubmatch(page)
			if chosen == nil {
				t.Fatal("GET /checkout chose no delivery method")
			}
			form := url.Values{
				"email": {"focus@example.com"}, "name": {"王小明"}, "phone": {"0912345678"},
				"postal_code": {"110"}, "city": {"台北市"}, "district": {"信義區"}, "street": {"松高路 1 號"},
				"coupon": {refusedCode}, "shipping": {chosen[1]}, "checkout_quote": {quote}, "idempotency": {attempt},
			}
			if tt.update != "" {
				form.Set("update", tt.update)
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout",
				strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tt.htmx {
				req.Header.Set("HX-Request", "true")
			}
			req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
			res := httptest.NewRecorder()
			h.PlaceOrder(res, req)

			if res.Code != tt.status {
				t.Fatalf("POST /checkout answered %d, want %d", res.Code, tt.status)
			}
			body := res.Body.String()
			if !strings.Contains(body, `id="coupon-error"`) {
				t.Fatal("the refused code is not shown beside the coupon field")
			}
			asked := autofocused.FindAllString(body, -1)
			if !tt.focused {
				if len(asked) != 0 {
					t.Errorf("%d elements ask for focus in an answer htmx swaps in, want none", len(asked))
				}
				return
			}
			if len(asked) != 1 || !autofocused.MatchString(codeField.FindString(body)) {
				t.Errorf("%d elements ask for focus, want the code's field alone", len(asked))
			}
		})
	}
}
