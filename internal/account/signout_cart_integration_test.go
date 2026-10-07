//go:build integration

package account_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/orderaccess"
	"github.com/koopa0/goen/internal/ratelimit"
)

// TestAnAccountCartAnswersOnlyToItsAccount is the shared computer: A adopts a
// guest cart by registering, signs out and leaves. The browser may still send
// the cart token, because a client can ignore an expiry, so the token alone
// must not reach A's cart for a signed-out visitor or for B signing in next.
func TestAnAccountCartAnswersOnlyToItsAccount(t *testing.T) {
	ctx := t.Context()
	appPool := accountStorePool(t, "account-cart-owner")
	carts := cart.NewHandler(cart.NewStore(appPool), orderaccess.NewStore(appPool, false), slog.New(slog.DiscardHandler), false,
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}),
		nil, nil)
	h := account.NewHandler(account.NewStore(appPool), carts, slog.New(slog.DiscardHandler), false, nil)
	// The storefront's order: the session, then the cart badge, then the route.
	serve := func(route http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.Authenticate(carts.WithCount(route)).ServeHTTP(rec, req)
		return rec
	}

	aVariant := sellableVariant(t, ctx)
	otherVariant := anotherSellableVariant(t, ctx, aVariant)
	aEmail := "cart-owner-" + uuid.NewString() + "@example.com"
	bEmail := "cart-next-" + uuid.NewString() + "@example.com"

	added := serve(carts.AddItem, cartForm(ctx, "/cart/items", url.Values{
		"variant": {aVariant.String()}, "quantity": {"1"},
	}))
	aToken := lastCartCookie(t, added).Value

	registerA := cartForm(ctx, "/register", registration(aEmail))
	registerA.AddCookie(browserCart(aToken))
	registeredA := serve(h.Register, registerA)
	if registeredA.Code != http.StatusSeeOther {
		t.Fatalf("A's registration status = %d, want 303; body=%s", registeredA.Code, registeredA.Body.String())
	}
	// A registration signs in, and adopts the cart, where its link is followed
	// with the password chosen at registration.
	completedA := followRegistrationLink(t, account.NewStore(appPool), aEmail,
		func(req *http.Request) *httptest.ResponseRecorder { return serve(h.CompleteRegistration, req) },
		browserCart(aToken))
	aSession := sessionCookie(t, completedA)
	var aCart uuid.UUID
	var aOwner uuid.NullUUID
	if err := pool.QueryRow(ctx, `SELECT id, user_id FROM carts WHERE token_hash = $1`,
		cart.HashToken(aToken)).Scan(&aCart, &aOwner); err != nil {
		t.Fatalf("read the adopted cart: %v", err)
	}
	if !aOwner.Valid {
		t.Fatal("registration did not adopt the guest cart; the fixture proves nothing")
	}

	signOut := cartForm(ctx, "/signout", url.Values{})
	signOut.AddCookie(aSession)
	signOut.AddCookie(browserCart(aToken))
	signedOut := serve(h.SignOut, signOut)
	if c := lastCartCookie(t, signedOut); c.MaxAge >= 0 {
		t.Errorf("sign-out left the cart cookie %q (Max-Age %d), want it expired", c.Value, c.MaxAge)
	}

	t.Run("a signed-out visitor does not see it", func(t *testing.T) {
		page := httptest.NewRequestWithContext(ctx, http.MethodGet, "/cart", http.NoBody)
		page.AddCookie(browserCart(aToken))
		shown := serve(carts.Page, page)
		if shown.Code != http.StatusOK {
			t.Fatalf("GET /cart status = %d, want 200", shown.Code)
		}
		if q := cartLineQuantity(shown.Body.String(), aVariant); q != "" {
			t.Errorf("a signed-out /cart shows A's line at quantity %s", q)
		}
		if c := lastCartCookie(t, shown); c.MaxAge >= 0 {
			t.Errorf("the stale cart cookie was kept (Max-Age %d), want it expired", c.MaxAge)
		}
	})

	t.Run("a guest's add opens its own cart", func(t *testing.T) {
		add := cartForm(ctx, "/cart/items", url.Values{
			"variant": {otherVariant.String()}, "quantity": {"1"},
		})
		add.AddCookie(browserCart(aToken))
		res := serve(carts.AddItem, add)
		assertCartHoldsOnly(t, aCart, aVariant)
		if c := lastCartCookie(t, res); c.Value == "" || c.Value == aToken || c.MaxAge <= 0 {
			t.Errorf("the guest's cart cookie is %q (Max-Age %d), want a fresh token", c.Value, c.MaxAge)
		}
	})

	registerB := cartForm(ctx, "/register", registration(bEmail))
	registerB.AddCookie(browserCart(aToken))
	if registeredB := serve(h.Register, registerB); registeredB.Code != http.StatusSeeOther {
		t.Fatalf("B's registration status = %d, want 303", registeredB.Code)
	}
	completedB := followRegistrationLink(t, account.NewStore(appPool), bEmail,
		func(req *http.Request) *httptest.ResponseRecorder { return serve(h.CompleteRegistration, req) },
		browserCart(aToken))
	if loc := completedB.Header().Get("Location"); loc != "/account?welcome=1" {
		t.Errorf("B's registration link lands at %q, want account welcome: there was no cart of B's to adopt", loc)
	}
	bSession := sessionCookie(t, completedB)

	t.Run("the next customer does not see it", func(t *testing.T) {
		page := httptest.NewRequestWithContext(ctx, http.MethodGet, "/cart", http.NoBody)
		page.AddCookie(bSession)
		page.AddCookie(browserCart(aToken))
		shown := serve(carts.Page, page)
		if q := cartLineQuantity(shown.Body.String(), aVariant); q != "" {
			t.Errorf("B's /cart shows A's line at quantity %s", q)
		}
	})

	t.Run("the next customer cannot check it out", func(t *testing.T) {
		place := cartForm(ctx, "/checkout", url.Values{})
		place.AddCookie(bSession)
		place.AddCookie(browserCart(aToken))
		res := serve(carts.PlaceOrder, place)
		if loc := res.Header().Get("Location"); res.Code != http.StatusSeeOther || loc != "/cart" {
			t.Errorf("B's checkout answered %d to %q, want 303 to /cart: B has no cart", res.Code, loc)
		}
		assertCartHoldsOnly(t, aCart, aVariant)
	})

	t.Run("the next customer's add opens their own cart", func(t *testing.T) {
		add := cartForm(ctx, "/cart/items", url.Values{
			"variant": {otherVariant.String()}, "quantity": {"1"},
		})
		add.AddCookie(bSession)
		add.AddCookie(browserCart(aToken))
		serve(carts.AddItem, add)
		assertCartHoldsOnly(t, aCart, aVariant)

		page := httptest.NewRequestWithContext(ctx, http.MethodGet, "/cart", http.NoBody)
		page.AddCookie(bSession)
		page.AddCookie(browserCart(aToken))
		body := serve(carts.Page, page).Body.String()
		if q := cartLineQuantity(body, otherVariant); q != "1" {
			t.Errorf("B's own line quantity = %q, want 1", q)
		}
		if q := cartLineQuantity(body, aVariant); q != "" {
			t.Errorf("B's /cart shows A's line at quantity %s", q)
		}
	})

	t.Run("A signs in again and has it", func(t *testing.T) {
		signIn := cartForm(ctx, "/signin", url.Values{
			"email": {aEmail}, "password": {cartOwnerPassword}, "next": {"/cart"},
		})
		signIn.AddCookie(browserCart(aToken))
		signedIn := serve(h.SignIn, signIn)
		if loc := signedIn.Header().Get("Location"); loc != "/cart" {
			t.Fatalf("A's sign-in redirect = %q, want /cart", loc)
		}
		page := httptest.NewRequestWithContext(ctx, http.MethodGet, "/cart", http.NoBody)
		page.AddCookie(sessionCookie(t, signedIn))
		page.AddCookie(browserCart(aToken))
		if q := cartLineQuantity(serve(carts.Page, page).Body.String(), aVariant); q != "1" {
			t.Errorf("A's own /cart line quantity = %q, want 1", q)
		}
	})
}

// TestSignOutLeavesAGuestCartWithTheBrowser is sign-in that did not adopt the
// browser's guest cart: the customer is signed in and shopping in a cart no
// account owns. That cart is the browser's, and the cookie the only way back to
// it, so sign-out leaves the cookie where it forgets an account's.
func TestSignOutLeavesAGuestCartWithTheBrowser(t *testing.T) {
	ctx := t.Context()
	appPool := accountStorePool(t, "account-guest-cart-kept")
	carts := cart.NewHandler(cart.NewStore(appPool), orderaccess.NewStore(appPool, false), slog.New(slog.DiscardHandler), false,
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}),
		nil, nil)
	h := account.NewHandler(account.NewStore(appPool), carts, slog.New(slog.DiscardHandler), false, nil)
	serve := func(route http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.Authenticate(carts.WithCount(route)).ServeHTTP(rec, req)
		return rec
	}

	variant := sellableVariant(t, ctx)
	added := serve(carts.AddItem, cartForm(ctx, "/cart/items", url.Values{
		"variant": {variant.String()}, "quantity": {"1"},
	}))
	guestToken := lastCartCookie(t, added).Value

	// Completing a registration without the cart cookie adopts nothing, which is
	// the state a failed adoption leaves: a session, and a cookie naming an
	// unowned cart.
	email := "guest-cart-kept-" + uuid.NewString() + "@example.com"
	registered := serve(h.Register, cartForm(ctx, "/register", registration(email)))
	if registered.Code != http.StatusSeeOther {
		t.Fatalf("registration status = %d, want 303; body=%s", registered.Code, registered.Body.String())
	}
	completed := followRegistrationLink(t, account.NewStore(appPool), email,
		func(req *http.Request) *httptest.ResponseRecorder { return serve(h.CompleteRegistration, req) })
	var owner uuid.NullUUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM carts WHERE token_hash = $1`,
		cart.HashToken(guestToken)).Scan(&owner); err != nil {
		t.Fatalf("read the guest cart: %v", err)
	}
	if owner.Valid {
		t.Fatal("registration adopted the guest cart; the fixture proves nothing")
	}

	signOut := cartForm(ctx, "/signout", url.Values{})
	signOut.AddCookie(sessionCookie(t, completed))
	signOut.AddCookie(browserCart(guestToken))
	signedOut := serve(h.SignOut, signOut)
	if signedOut.Code != http.StatusSeeOther {
		t.Fatalf("sign-out status = %d, want 303", signedOut.Code)
	}
	for _, c := range signedOut.Result().Cookies() {
		if c.Name == "goen_cart" {
			t.Errorf("sign-out set the cart cookie to %q (Max-Age %d); the browser's own "+
				"guest cart would be lost", c.Value, c.MaxAge)
		}
	}

	page := httptest.NewRequestWithContext(ctx, http.MethodGet, "/cart", http.NoBody)
	page.AddCookie(browserCart(guestToken))
	if q := cartLineQuantity(serve(carts.Page, page).Body.String(), variant); q != "1" {
		t.Errorf("the signed-out browser's cart line quantity = %q, want 1", q)
	}
}

const cartOwnerPassword = "a sufficiently long password"

func registration(email string) url.Values {
	return url.Values{
		"email":    {email},
		"password": {cartOwnerPassword},
		"confirm":  {cartOwnerPassword},
		"name":     {"購物車測試"},
		"next":     {"/account"},
	}
}

func cartForm(ctx context.Context, target string, form url.Values) *http.Request {
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

//nolint:gosec // G124: the development cart cookie a browser sends back
func browserCart(token string) *http.Cookie {
	return &http.Cookie{Name: "goen_cart", Value: token}
}

// lastCartCookie is the cart cookie a browser keeps from rec: Set-Cookie lines
// apply in order, so an expiry followed by a fresh token leaves the token.
func lastCartCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	var last *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "goen_cart" {
			last = c
		}
	}
	if last == nil {
		t.Fatalf("response set no cart cookie; cookies = %v", rec.Result().Cookies())
	}
	return last
}

// assertCartHoldsOnly holds A's cart at the one line A put in it: a line more
// is somebody else's write, and a line fewer is somebody else's checkout.
func assertCartHoldsOnly(t *testing.T, cartID, variant uuid.UUID) {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT variant_id, quantity FROM cart_items WHERE cart_id = $1 ORDER BY variant_id`, cartID)
	if err != nil {
		t.Fatalf("read cart lines: %v", err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var v uuid.UUID
		var q int32
		if err := rows.Scan(&v, &q); err != nil {
			t.Fatalf("scan cart line: %v", err)
		}
		lines = append(lines, fmt.Sprintf("%s×%d", v, q))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read cart lines: %v", err)
	}
	if want := []string{fmt.Sprintf("%s×1", variant)}; !slices.Equal(lines, want) {
		t.Errorf("A's cart holds %v, want %v", lines, want)
	}
}
