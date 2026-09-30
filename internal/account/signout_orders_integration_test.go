//go:build integration

package account_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/ratelimit"
)

// TestSigningOutEndsTheBrowsersAccessToItsOrders is the shared computer again:
// the browser holds proof of access to an order placed signed in and to one
// placed as a guest before, each a page with a name, an address and a phone
// number and buttons that cancel, pay and return. Signing out must leave the
// next person at it no way into either. The browser may still send the old
// cookie, because a client can ignore an expiry, so the proof itself has to
// stop working; proving access again, through the order lookup, still works.
func TestSigningOutEndsTheBrowsersAccessToItsOrders(t *testing.T) {
	ctx := t.Context()
	appPool := accountStorePool(t, "signout-orders")
	shop := cart.NewStore(appPool)
	carts := cart.NewHandler(shop, slog.New(slog.DiscardHandler), false,
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}),
		nil, nil)
	accounts := account.NewStore(appPool)
	h := account.NewHandler(accounts, carts, slog.New(slog.DiscardHandler), false, nil)
	serve := func(route http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.Authenticate(carts.WithCount(route)).ServeHTTP(rec, req)
		return rec
	}

	addr := "signout-orders-" + uuid.NewString() + "@example.com"
	u := registerProved(t, accounts, addr)
	owner := uuid.NullUUID{UUID: uuid.MustParse(u.ID), Valid: true}

	var shippingID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT v.id FROM shipping_method_versions v JOIN shipping_methods m ON m.id = v.method_id
		WHERE m.code = 'home_delivery' ORDER BY v.effective_at DESC, v.id DESC LIMIT 1`).Scan(&shippingID); err != nil {
		t.Fatalf("read the delivery method: %v", err)
	}
	variant := sellableVariant(t, ctx)
	place := func(buyer uuid.NullUUID, previous *http.Cookie) (string, *http.Cookie) {
		t.Helper()
		token, err := cart.NewToken()
		if err != nil {
			t.Fatalf("cart token: %v", err)
		}
		cartID, err := shop.Create(ctx, token, buyer)
		if err != nil {
			t.Fatalf("create cart: %v", err)
		}
		if err = shop.Add(ctx, cartID, variant, 1); err != nil {
			t.Fatalf("add to cart: %v", err)
		}
		address := &cart.Address{Email: addr, Name: "登出測試", Phone: "0912345678",
			PostalCode: "110", City: "台北市", District: "信義區", Street: "松仁路 1 號"}
		number, err := shop.PlaceOrder(ctx, cartID, buyer, shippingID, address, nil, "",
			accountCheckoutQuote(t, shop, cartID, buyer, shippingID, address.PostalCode),
			checkoutAttemptKey("signout-orders-"+uuid.NewString()))
		if err != nil {
			t.Fatalf("place order: %v", err)
		}
		// The browser's side of placing it: the order joins its placed cookie.
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/checkout", http.NoBody)
		if previous != nil {
			req.AddCookie(previous)
		}
		rec := httptest.NewRecorder()
		if err := shop.RememberOrder(ctx, rec, req, number, false); err != nil {
			t.Fatalf("remember order: %v", err)
		}
		return number, placedCookie(t, rec)
	}
	guestOrder, placed := place(uuid.NullUUID{}, nil)
	accountOrder, placed := place(owner, placed)

	opens := func(number string, cookies ...*http.Cookie) int {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number, http.NoBody)
		req.SetPathValue("number", number)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		return serve(carts.OrderPage, req).Code
	}
	for _, number := range []string{guestOrder, accountOrder} {
		if code := opens(number, placed); code != http.StatusOK {
			t.Fatalf("order %s answered %d to the browser that placed it, want 200; "+
				"the fixture proves nothing", number, code)
		}
	}

	session, err := accounts.StartSession(ctx, u.ID, "signout-orders", "127.0.0.1")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	signOut := cartForm(ctx, "/signout", url.Values{})
	//nolint:gosec // G124: the development session cookie a browser sends back
	signOut.AddCookie(&http.Cookie{Name: "goen_session", Value: session})
	signOut.AddCookie(placed)
	signedOut := serve(h.SignOut, signOut)
	if c := placedCookie(t, signedOut); c.MaxAge >= 0 {
		t.Errorf("sign-out left the placed cookie %q (Max-Age %d), want it expired", c.Value, c.MaxAge)
	}

	for _, number := range []string{guestOrder, accountOrder} {
		if code := opens(number, placed); code != http.StatusNotFound {
			t.Errorf("after sign-out, order %s answered %d to the old cookie, want 404", number, code)
		}
	}

	found := serve(carts.FindOrder, cartForm(ctx, "/orders/find", url.Values{
		"number": {guestOrder}, "email": {addr},
	}))
	if found.Code != http.StatusSeeOther {
		t.Fatalf("finding the order again answered %d, want 303", found.Code)
	}
	if code := opens(guestOrder, placedCookie(t, found)); code != http.StatusOK {
		t.Errorf("an order found again answered %d, want 200", code)
	}
}

// placedCookie is the placed-orders cookie a browser keeps from rec.
func placedCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	var last *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "goen_placed" {
			last = c
		}
	}
	if last == nil {
		t.Fatalf("response set no placed cookie; cookies = %v", rec.Result().Cookies())
	}
	return last
}
