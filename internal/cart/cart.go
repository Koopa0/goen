// Package cart is a shopper's cart, the variants they mean to buy and how many
// of each; it holds adding, changing and removing lines, the cart page,
// carrying a guest cart into an account at sign-in, reordering a past order
// into the cart and the sweep of idle guest carts; it does not hold turning the
// cart into an order (checkout) or what the cart reports to the pages that
// show its items elsewhere (cart/cartitem). A cart is identified by a cookie
// whose random token the database stores only as a SHA-256 digest.
package cart

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var (
	ErrNotFound = errors.New("cart: not found")
	// ErrNotYourCart is a token naming another account's cart, or any account's
	// when nobody is signed in.
	ErrNotYourCart = errors.New("cart: the cart belongs to an account the requester is not signed in as")
	ErrUnavailable = errors.New("cart: variant unavailable")
	// ErrTooManyItems means no room for another distinct product within ECPay's
	// Items limit per invoice.
	ErrTooManyItems = errors.New("cart: too many invoice items")
	// ErrQuantityAdjusted means the write succeeded but kept less than
	// requested: stock fell short or the line reached MaxLineQuantity, or, in
	// adoption, a guest line found no room for another distinct product.
	ErrQuantityAdjusted = errors.New("cart: quantity adjusted to available stock")
	// ErrMergeRefused means a guest line cannot be adopted because the catalogue
	// no longer honours it; the transaction rolls back with both carts unchanged.
	ErrMergeRefused = errors.New("cart: guest cart contains unavailable merchandise")
)

// CookieName carries the __Host- prefix, which binds it to this origin, so a
// sibling subdomain cannot write a cart cookie goen would trust.
const CookieName = "__Host-goen_cart"

// GuestCartRetain is how long a guest cart lives, and it is the cart cookie's
// lifetime: the cookie is set once, when the cart is created, so after this long
// no browser can name the cart and [Store.SweepGuestCarts] deletes it.
const GuestCartRetain = 30 * 24 * time.Hour

const cookieMaxAge = int(GuestCartRetain / time.Second)

// MaxLineQuantity matches the schema's CHECK.
const MaxLineQuantity = 999

// SetCookie writes the cart cookie; a __Host- cookie without Secure is rejected by the browser, so
// development uses a different name.
func SetCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: dev-only opt-out, secure by default
		Name:     cookieName(secure),
		Value:    token,
		Path:     "/",
		MaxAge:   cookieMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// A browser replaces a cookie only with one of the same name and path, and
// refuses a __Host- name without Secure; expireCookie sends both set-time
// attributes.
func expireCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: dev-only opt-out, secure by default
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func ReadCookie(r *http.Request, secure bool) string {
	c, err := r.Cookie(cookieName(secure))
	if err != nil {
		return ""
	}
	return c.Value
}

func cookieName(secure bool) string {
	if secure {
		return CookieName
	}
	return "goen_cart"
}

// ParseQuantity clamps to what a line may hold; unparseable is one item,
// because the button means "add this".
func ParseQuantity(s string) int32 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
	switch {
	case err != nil, n < 1:
		return 1
	case n > MaxLineQuantity:
		return MaxLineQuantity
	}
	return int32(n)
}

// ParseQuantityAllowingZero is the cart page's version, where zero removes the
// line.
func ParseQuantityAllowingZero(s string) (int32, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
	switch {
	case err != nil, n < 0:
		return 0, false
	case n > MaxLineQuantity:
		return MaxLineQuantity, true
	}
	return int32(n), true
}

// ShippingFee treats a free-over threshold of zero as never free. The cart
// prices its delivery choices with it and the checkout its quote.
func ShippingFee(feeCents, freeOverCents, subtotalCents int64) int64 {
	if freeOverCents > 0 && subtotalCents >= freeOverCents {
		return 0
	}
	return feeCents
}
