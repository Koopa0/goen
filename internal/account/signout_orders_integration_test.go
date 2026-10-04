//go:build integration

package account_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/user"
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

// placedOrders is a browser holding proof of access to a guest order it placed
// and to an order it placed signed in as u, served by the router's own
// Authenticate and cart middleware.
type placedOrders struct {
	t        *testing.T
	h        *account.Handler
	carts    *cart.Handler
	accounts *account.Store
	u        user.User
	orders   []string
	placed   *http.Cookie
}

func newPlacedOrders(t *testing.T, name string) *placedOrders {
	t.Helper()
	ctx := t.Context()
	appPool := accountStorePool(t, name)
	shop := cart.NewStore(appPool)
	p := &placedOrders{t: t, accounts: account.NewStore(appPool)}
	p.carts = cart.NewHandler(shop, slog.New(slog.DiscardHandler), false,
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}),
		nil, nil)
	p.h = account.NewHandler(p.accounts, p.carts, slog.New(slog.DiscardHandler), false, nil)
	addr := name + "-" + uuid.NewString() + "@example.com"
	p.u = registerProved(t, p.accounts, addr)

	var shippingID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT v.id FROM shipping_method_versions v JOIN shipping_methods m ON m.id = v.method_id
		WHERE m.code = 'home_delivery' ORDER BY v.effective_at DESC, v.id DESC LIMIT 1`).Scan(&shippingID); err != nil {
		t.Fatalf("read the delivery method: %v", err)
	}
	variant := ownVariant(t, name)
	for _, buyer := range []uuid.NullUUID{{}, {UUID: uuid.MustParse(p.u.ID), Valid: true}} {
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
			checkoutAttemptKey(name+"-"+uuid.NewString()))
		if err != nil {
			t.Fatalf("place order: %v", err)
		}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/checkout", http.NoBody)
		if p.placed != nil {
			req.AddCookie(p.placed)
		}
		rec := httptest.NewRecorder()
		if err := shop.RememberOrder(ctx, rec, req, number, false); err != nil {
			t.Fatalf("remember order: %v", err)
		}
		p.orders = append(p.orders, number)
		p.placed = placedCookie(t, rec)
	}
	for _, number := range p.orders {
		if code := p.opens(number, p.placed); code != http.StatusOK {
			t.Fatalf("order %s answered %d to the browser that placed it, want 200; "+
				"the fixture proves nothing", number, code)
		}
	}
	return p
}

// ownVariant is a published product of the calling test's own with one variant
// in stock. The suite shuffles, and orders held on a shared variant take the
// stock other tests count on.
func ownVariant(t *testing.T, name string) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	slug := name + "-" + uuid.NewString()[:8]
	var productID, variantID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		SELECT b.id, c.id, $1, $1, 'draft', NULL
		FROM brands b, categories c
		ORDER BY b.id, c.id LIMIT 1
		RETURNING id`, slug).Scan(&productID); err != nil {
		t.Fatalf("create product %s: %v", slug, err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO product_variants (product_id, sku, price_cents, is_active, position)
		VALUES ($1, $2, 199900, true, 0) RETURNING id`,
		productID, strings.ToUpper(slug)).Scan(&variantID); err != nil {
		t.Fatalf("create variant: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`SELECT record_inventory_movement($1, 10, 'adjustment', $2, 'admin', NULL, NULL)`,
		variantID, "fixture:"+variantID.String()); err != nil {
		t.Fatalf("stock the variant: %v", err)
	}
	// Published only now: products_active_has_variant is deferred and fires from
	// both sides.
	if _, err := pool.Exec(ctx,
		`UPDATE products SET status = 'active', published_at = now() WHERE id = $1`, productID); err != nil {
		t.Fatalf("publish %s: %v", slug, err)
	}
	return variantID
}

func (p *placedOrders) serve(route http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	p.h.Authenticate(p.carts.WithCount(route)).ServeHTTP(rec, req)
	return rec
}

func (p *placedOrders) opens(number string, cookies ...*http.Cookie) int {
	p.t.Helper()
	req := httptest.NewRequestWithContext(p.t.Context(), http.MethodGet, "/orders/"+number, http.NoBody)
	req.SetPathValue("number", number)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	return p.serve(p.carts.OrderPage, req).Code
}

// signIn starts a session for p.u and returns the cookie a browser holds for it.
func (p *placedOrders) signIn() *http.Cookie {
	p.t.Helper()
	session, err := p.accounts.StartSession(p.t.Context(), p.u.ID, "session-end", "127.0.0.1")
	if err != nil {
		p.t.Fatalf("start session: %v", err)
	}
	//nolint:gosec // G124: the development session cookie a browser sends back
	return &http.Cookie{Name: "goen_session", Value: session}
}

// forgotten fails the test unless rec expired the placed cookie and the old
// cookie now opens neither order.
func (p *placedOrders) forgotten(step string, rec *httptest.ResponseRecorder) {
	p.t.Helper()
	if c := placedCookie(p.t, rec); c.MaxAge >= 0 {
		p.t.Errorf("%s: the answer left the placed cookie %q (Max-Age %d), want it expired", step, c.Value, c.MaxAge)
	}
	for _, number := range p.orders {
		if code := p.opens(number, p.placed); code != http.StatusNotFound {
			p.t.Errorf("%s: order %s answered %d to the old cookie, want 404", step, number, code)
		}
	}
}

// TestASessionEndedFromAnotherDeviceEndsTheBrowsersAccessToItsOrders: the
// browser never signs out, and the next request that brings its cookie finds
// the session gone. The browser keeps no more of the orders than sign-out
// would leave it.
func TestASessionEndedFromAnotherDeviceEndsTheBrowsersAccessToItsOrders(t *testing.T) {
	p := newPlacedOrders(t, "session-end")
	session := p.signIn()
	if err := p.accounts.EndSession(t.Context(), session.Value); err != nil {
		t.Fatalf("end the session: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	req.AddCookie(session)
	req.AddCookie(p.placed)
	p.forgotten("the next request", p.serve(noContent, req))
}

// TestASessionThatExpiresEndsTheBrowsersAccessToItsOrders is the browser a
// session expires in, which sends a cookie only while its Max-Age lasts. An
// order placed at the session's last moment keeps its proof for the placed
// cookie's whole life after that, and until that proof's last day the browser
// must still present the session's cookie: nothing else tells goen the session
// it was proof for has ended.
func TestASessionThatExpiresEndsTheBrowsersAccessToItsOrders(t *testing.T) {
	ctx := t.Context()
	p := newPlacedOrders(t, "session-expiry")
	signedIn := httptest.NewRecorder()
	p.h.SignIn(signedIn, cartForm(ctx, "/signin", url.Values{
		"email": {p.u.Email}, "password": {"a sufficiently long password"}, "next": {"/account"},
	}))
	session := sessionCookie(t, signedIn)
	if _, err := pool.Exec(ctx, `
		UPDATE sessions SET created_at = now() - interval '15 days',
		                    expires_at = now() - interval '1 second'
		WHERE token_hash = $1`, account.HashToken(session.Value)); err != nil {
		t.Fatalf("expire the session: %v", err)
	}

	proofLastSent := time.Duration(account.SessionTTL+p.placed.MaxAge)*time.Second - time.Second
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody)
	if time.Duration(session.MaxAge)*time.Second > proofLastSent {
		req.AddCookie(&http.Cookie{Name: session.Name, Value: session.Value}) //nolint:gosec // G124: the name and value a browser sends back
	}
	req.AddCookie(p.placed)
	p.forgotten("on the last day the placed cookie is sent", p.serve(noContent, req))
}

func noContent(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

// TestAStaleSessionAtErasureEndsTheBrowsersAccessToItsOrders: erasure asks a
// session older than its window to sign in again and ends it first, which is a
// session ending like any other.
func TestAStaleSessionAtErasureEndsTheBrowsersAccessToItsOrders(t *testing.T) {
	p := newPlacedOrders(t, "erase-stale")
	session := p.signIn()
	if _, err := pool.Exec(t.Context(), `
		UPDATE sessions SET created_at = now() - interval '1 day' WHERE token_hash = $1`,
		account.HashToken(session.Value)); err != nil {
		t.Fatalf("age the session: %v", err)
	}
	req := cartForm(t.Context(), "/account/erase", url.Values{"confirm": {p.u.Email}})
	req.AddCookie(session)
	req.AddCookie(p.placed)
	rec := p.serve(p.h.Erase, req)
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/signin?") {
		t.Fatalf("erasure with a stale session answered %d to %q, want the sign-in page", rec.Code, loc)
	}
	p.forgotten("erasure with a stale session", rec)
}

// TestABrowserThatNeverSignedInKeepsItsOrders: a guest's own proof is its only
// way back to its orders, and nothing here ends a session it never had.
func TestABrowserThatNeverSignedInKeepsItsOrders(t *testing.T) {
	p := newPlacedOrders(t, "never-signed-in")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	req.AddCookie(p.placed)
	rec := p.serve(noContent, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == "goen_placed" {
			t.Errorf("a browser that never signed in was sent %s (Max-Age %d)", c.Name, c.MaxAge)
		}
	}
	for _, number := range p.orders {
		if code := p.opens(number, p.placed); code != http.StatusOK {
			t.Errorf("order %s answered %d to the browser that placed it, want 200", number, code)
		}
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
