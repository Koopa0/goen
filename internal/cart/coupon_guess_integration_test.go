//go:build integration

package cart_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
)

// TestWrongCouponCodesAreBoundedWithoutSayingWhichCodeWasRight: a wrong code and
// a right one answer differently at checkout, so each shopper may be told a
// code is wrong only so many times. The refusal comes before the lookup, so a
// shopper who has run out learns nothing about the next code, right or wrong,
// and a new cart from the same client starts with nothing.
func TestWrongCouponCodesAreBoundedWithoutSayingWhichCodeWasRight(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "coupon-guess")
	valid := "GUESSRIGHT" + strings.ToUpper(uuid.NewString()[:6])
	coupon(t, valid, "amount", 1000, 0, 0, 0, 0)
	ship := shipVersionFor(t, "home_delivery")
	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false,
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}),
		nil, nil)

	guestCart := func() string {
		token, err := cart.NewToken()
		if err != nil {
			t.Fatalf("token: %v", err)
		}
		id, err := s.Create(ctx, token, uuid.NullUUID{})
		if err != nil {
			t.Fatalf("create cart: %v", err)
		}
		if err := s.Add(ctx, id, vid, 1); err != nil {
			t.Fatalf("add item: %v", err)
		}
		return token
	}
	// A chooser change: the coupon is looked up and the page re-rendered, and
	// nothing is placed, which is the cheapest way to ask about a code.
	ask := func(token, remote, code string) *httptest.ResponseRecorder {
		form := url.Values{"coupon": {code}, "update": {"shipping"}, "shipping": {ship.String()}}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/checkout",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = remote
		req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cart cookie under test
		res := httptest.NewRecorder()
		h.PlaceOrder(res, req)
		return res
	}
	unknown := i18n.T(ctx, i18n.KeyCouponUnknown)

	shopper := guestCart()
	const client = "198.51.100.20:5000"
	for i := range 3 {
		res := ask(shopper, client, "TYPO"+strconv.Itoa(i))
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), unknown) {
			t.Fatalf("mistyped code %d answered %d without the unknown-code message", i+1, res.Code)
		}
	}
	// A code that applies is never charged, however often the page re-renders.
	for i := range 30 {
		res := ask(shopper, client, valid)
		if res.Code != http.StatusOK || strings.Contains(res.Body.String(), unknown) {
			t.Fatalf("re-render %d with a code that applies answered %d", i+1, res.Code)
		}
	}

	refusedAfter := 0
	for i := range 40 {
		if res := ask(shopper, client, "WRONG"+strconv.Itoa(i)); res.Code == http.StatusTooManyRequests {
			refusedAfter = i
			break
		}
	}
	if refusedAfter == 0 {
		t.Fatal("43 wrong codes from one cart were never refused")
	}
	for name, code := range map[string]string{"a wrong code": "WRONGAGAIN", "the right code": valid} {
		res := ask(shopper, client, code)
		if res.Code != http.StatusTooManyRequests {
			t.Errorf("once refused, %s answered %d; the answer must not depend on the code", name, res.Code)
		}
		if res.Header().Get("Retry-After") == "" {
			t.Errorf("the refusal of %s carries no Retry-After", name)
		}
	}
	if res := ask(shopper, client, ""); res.Code == http.StatusTooManyRequests {
		t.Error("a checkout with no code was refused; only codes are charged")
	}

	if res := ask(guestCart(), client, "WRONGFRESHCART"); res.Code != http.StatusTooManyRequests {
		t.Errorf("a fresh cart from the same client answered %d, want 429: a new cart "+
			"must not buy a new allowance", res.Code)
	}
	if res := ask(guestCart(), "198.51.100.21:5000", "WRONGELSEWHERE"); res.Code != http.StatusOK {
		t.Errorf("another client with its own cart answered %d, want 200", res.Code)
	}
}
