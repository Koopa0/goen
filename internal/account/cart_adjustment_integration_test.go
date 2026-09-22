//go:build integration

package account_test

import (
	"fmt"
	"html"
	"io"
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
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
)

func TestCartAdjustmentSurvivesAuthenticationAndCheckout(t *testing.T) {
	for _, mode := range []string{"signin", "register", "google", "retry"} {
		for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
			for _, next := range []string{"/cart", "/cart#items", "/cart?source=signin#items", "/checkout?source=signin#address", "/p/headphones?variant=blue#buy", "/account"} {
				t.Run(mode+"/"+string(locale)+next, func(t *testing.T) {
					ctx := i18n.WithLocale(t.Context(), locale)
					appPool := accountStorePool(t, "cart-adjustment")
					accounts := account.NewStore(appPool)
					suffix := uuid.NewString()
					email := "adjust-" + suffix + "@example.com"
					password := "a sufficiently long password"
					var uid, productID, variantID uuid.UUID
					if err := pool.QueryRow(ctx, `INSERT INTO products (slug, name, brand_id, category_id)
      SELECT $1, 'Adjustment fixture', b.id, c.id FROM brands b, categories c
      WHERE b.slug='koto' AND c.parent_id IS NULL ORDER BY c.position LIMIT 1 RETURNING id`, "adjust-"+suffix).Scan(&productID); err != nil {
						t.Fatal(err)
					}
					if err := pool.QueryRow(ctx, `INSERT INTO product_variants(product_id,sku,price_cents,safety_stock) VALUES($1,$2,100000,0) RETURNING id`, productID, "ADJ-"+strings.ToUpper(suffix)).Scan(&variantID); err != nil {
						t.Fatal(err)
					}
					if _, err := pool.Exec(ctx, `SELECT record_inventory_movement($1,3,'receipt',$2,NULL,NULL)`, variantID, "adjust-"+suffix); err != nil {
						t.Fatal(err)
					}
					if _, err := pool.Exec(ctx, `UPDATE products SET status='active', published_at=now() WHERE id=$1`, productID); err != nil {
						t.Fatal(err)
					}
					var accountCart, guestCart uuid.UUID
					guestQuantity := 5
					if mode == "signin" || mode == "retry" {
						u := register(t, accounts, email)
						uid = uuid.MustParse(u.ID)
						if err := pool.QueryRow(ctx, `INSERT INTO carts(token_hash,user_id) VALUES($1,$2) RETURNING id`, account.HashToken("account-"+suffix), uid).Scan(&accountCart); err != nil {
							t.Fatal(err)
						}
						if _, err := pool.Exec(ctx, `INSERT INTO cart_items(cart_id,variant_id,quantity) VALUES($1,$2,2)`, accountCart, variantID); err != nil {
							t.Fatal(err)
						}
						guestQuantity = 2
					}
					guestToken := "guest-" + suffix
					if err := pool.QueryRow(ctx, `INSERT INTO carts(token_hash) VALUES($1) RETURNING id`, account.HashToken(guestToken)).Scan(&guestCart); err != nil {
						t.Fatal(err)
					}
					if _, err := pool.Exec(ctx, `INSERT INTO cart_items(cart_id,variant_id,quantity) VALUES($1,$2,$3)`, guestCart, variantID, guestQuantity); err != nil {
						t.Fatal(err)
					}
					log := slog.New(slog.DiscardHandler)
					carts := cart.NewHandler(cart.NewStore(appPool), log, false, ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}), nil, nil)
					google, googleErr := account.NewGoogle("client-id", "client-secret", "https://goen.example")
					if googleErr != nil {
						t.Fatal(googleErr)
					}
					account.SetGoogleHTTPClient(google, &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
						body := `{"access_token":"fixture"}`
						if r.URL.Path == "/v1/userinfo" {
							body = fmt.Sprintf(`{"sub":%q,"email":%q,"email_verified":true,"name":"Fixture"}`, suffix, email)
						}
						return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
					})})
					h := account.NewHandler(accounts, carts, log, false, google)
					form := url.Values{"email": {email}, "password": {password}, "confirm": {password}, "name": {"Fixture"}, "next": {next}}
					req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/"+mode, strings.NewReader(form.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					var session *http.Cookie
					if mode == "retry" {
						token, sessionErr := accounts.StartSession(ctx, uid.String(), "fixture", "127.0.0.1")
						if sessionErr != nil {
							t.Fatal(sessionErr)
						}
						rec := httptest.NewRecorder()
						account.SetSessionCookie(rec, token, false)
						session = sessionCookie(t, rec)
						req.AddCookie(session)
					}
					if mode == "google" {
						start := httptest.NewRecorder()
						h.GoogleSignIn(start, httptest.NewRequestWithContext(ctx, http.MethodGet, "/auth/google?next="+url.QueryEscape(next), http.NoBody))
						location, parseErr := url.Parse(start.Header().Get("Location"))
						if parseErr != nil {
							t.Fatal(parseErr)
						}
						req = httptest.NewRequestWithContext(ctx, http.MethodGet, "/auth/google/callback?code=fixture&state="+url.QueryEscape(location.Query().Get("state")), http.NoBody)
						for _, cookie := range start.Result().Cookies() {
							req.AddCookie(cookie)
						}
					}
					//nolint:gosec // G124: the fixture is the browser's guest-cart cookie.
					req.AddCookie(&http.Cookie{Name: "goen_cart", Value: guestToken})
					signed := httptest.NewRecorder()
					switch mode {
					case "signin":
						h.SignIn(signed, req)
					case "register":
						h.Register(signed, req)
					case "google":
						h.GoogleCallback(signed, req)
					case "retry":
						h.Authenticate(http.HandlerFunc(h.RetryCartAdoption)).ServeHTTP(signed, req)
					}
					if signed.Code != http.StatusSeeOther {
						t.Fatalf("authentication status=%d body=%s", signed.Code, signed.Body.String())
					}
					if session == nil {
						session = sessionCookie(t, signed)
					}
					if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, email).Scan(&uid); err != nil {
						t.Fatal(err)
					}
					if err := pool.QueryRow(ctx, `SELECT id FROM carts WHERE user_id=$1`, uid).Scan(&accountCart); err != nil {
						t.Fatal(err)
					}
					if quantity := cartItemQuantity(t, accountCart, variantID); quantity != 3 {
						t.Fatalf("quantity=%d, want 3", quantity)
					}
					location, locationErr := url.Parse(signed.Header().Get("Location"))
					if locationErr != nil {
						t.Fatal(locationErr)
					}
					if location.Path != "/cart" {
						t.Fatalf("adjustment lands at %s, want cart", location)
					}
					target, _ := url.Parse(next)
					if target.Path == "/cart" && (location.Fragment != target.Fragment || location.Query().Get("source") != target.Query().Get("source")) {
						t.Fatalf("redirect lost query/fragment: %s", location)
					}
					request := httptest.NewRequestWithContext(ctx, http.MethodGet, location.RequestURI(), http.NoBody)
					request.AddCookie(session)
					//nolint:gosec // G124: the browser retains its original cart cookie after adoption.
					request.AddCookie(&http.Cookie{Name: "goen_cart", Value: guestToken})
					shown := httptest.NewRecorder()
					h.Authenticate(http.HandlerFunc(carts.Page)).ServeHTTP(shown, request)
					if shown.Code != http.StatusOK {
						t.Fatalf("cart status=%d", shown.Code)
					}
					if !strings.Contains(shown.Body.String(), i18n.T(ctx, i18n.KeyCartQuantityAdjusted)) {
						t.Fatalf("adjusted quantity=3 but notice missing: %s", location)
					}
					if target.Path != "/cart" && !strings.Contains(shown.Body.String(), `href="`+html.EscapeString(next)+`"`) {
						t.Fatalf("cart lost continuation %q", next)
					}
					var shippingID uuid.UUID
					if err := pool.QueryRow(ctx, `SELECT v.id FROM shipping_method_versions v JOIN shipping_methods m ON m.id=v.method_id WHERE m.code='home_delivery' ORDER BY v.effective_at DESC,v.id DESC LIMIT 1`).Scan(&shippingID); err != nil {
						t.Fatal(err)
					}
					owner := uuid.NullUUID{UUID: uid, Valid: true}
					address := &cart.Address{Email: email, Name: "Fixture", Phone: "0912345678", PostalCode: "110", City: "Taipei", District: "Xinyi", Street: "1 Test Road"}
					store := cart.NewStore(appPool)
					quote := accountCheckoutQuote(t, store, accountCart, owner, shippingID, address.PostalCode)
					number, err := store.PlaceOrder(ctx, accountCart, owner, shippingID, address, nil, "", quote, checkoutAttemptKey(suffix))
					if err != nil {
						t.Fatalf("checkout of adjusted cart: %v", err)
					}
					var ordered int
					if err := pool.QueryRow(ctx, `SELECT li.quantity FROM order_lines li JOIN orders o ON o.id=li.order_id WHERE o.order_number=$1 AND li.variant_id=$2`, number, variantID).Scan(&ordered); err != nil {
						t.Fatal(err)
					}
					if ordered != 3 {
						t.Fatalf("ordered quantity=%d, want 3", ordered)
					}
				})
			}
		}
	}
}
