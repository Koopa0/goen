//go:build integration

package cart_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/orderaccess"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/user"
)

// TestAStripeReturnIsLoadedAgainOnlyWhenTheGateRefusesACrossSiteNavigation
// covers the cookie columns the unit test cannot: an owner signed in, a
// browser holding the order's grant, both and neither, on the two pages Stripe
// sends a customer back to. A request the gate lets through is the page itself
// whatever Sec-Fetch-Site says; a refused one is loaded again only when it
// arrived cross-site.
func TestAStripeReturnIsLoadedAgainOnlyWhenTheGateRefusesACrossSiteNavigation(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	access := orderaccess.NewStore(pool, false)
	log := slog.New(slog.DiscardHandler)
	orders := cart.NewHandler(s, access, log, false, testLimiter(), nil, nil)
	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatalf("build disabled payment gateway: %v", err)
	}
	payments := payment.NewHandler(payment.NewStore(pool), gateway, access, log)

	owner, number := placeOwnedOrder(t, s)
	grant := placedCookie(t, number)
	missing := "ZZ" + number[2:]

	routes := []struct {
		name   string
		suffix string
		serve  http.HandlerFunc
	}{
		{"the order page Stripe returns to", "?paid=1", orders.OrderPage},
		{"the pay page Stripe cancels to", "/pay?cancelled=1", payments.Page},
	}
	get := func(serve http.HandlerFunc, number, suffix, fetchSite string, signedIn, granted bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number+suffix, http.NoBody)
		req.SetPathValue("number", number)
		if fetchSite != "" {
			req.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		if signedIn {
			req = req.WithContext(user.NewContext(req.Context(), user.User{
				ID: owner.String(), Role: user.RoleCustomer,
			}))
		}
		if granted {
			req.AddCookie(grant)
		}
		res := httptest.NewRecorder()
		serve(res, req)
		return res
	}

	for _, route := range routes {
		target := "/orders/" + number + route.suffix
		for _, fetchSite := range []string{"cross-site", "same-site", "same-origin", "none", ""} {
			for _, signedIn := range []bool{true, false} {
				for _, granted := range []bool{true, false} {
					res := get(route.serve, number, route.suffix, fetchSite, signedIn, granted)
					reloaded := res.Header().Get("Refresh") == "0; url="+target
					where := fmt.Sprintf("%s, Sec-Fetch-Site %q, owner signed in %v, grant held %v",
						route.name, fetchSite, signedIn, granted)
					switch {
					case signedIn || granted:
						if res.Code != http.StatusOK || reloaded {
							t.Errorf("%s: status %d, loaded again %v; want the page itself", where, res.Code, reloaded)
						}
					case fetchSite == "cross-site":
						if res.Code != http.StatusOK || !reloaded {
							t.Errorf("%s: status %d, Refresh %q; want it loaded again", where, res.Code, res.Header().Get("Refresh"))
						}
					default:
						if res.Code != http.StatusNotFound || res.Header().Get("Refresh") != "" {
							t.Errorf("%s: status %d, Refresh %q; want 404", where, res.Code, res.Header().Get("Refresh"))
						}
					}
				}
			}
		}

		// The two numbers have the same length, so with one written over the
		// other the answers must match byte for byte.
		existing := get(route.serve, number, route.suffix, "cross-site", false, false)
		absent := get(route.serve, missing, route.suffix, "cross-site", false, false)
		if got, want := strings.ReplaceAll(absent.Body.String(), missing, number), existing.Body.String(); got != want {
			t.Errorf("%s: the cross-site answer differs for an order that does not exist:\n got %s\nwant %s", route.name, got, want)
		}
		if absent.Code != existing.Code {
			t.Errorf("%s: status %d for a missing order, %d for an existing one", route.name, absent.Code, existing.Code)
		}
		for _, name := range []string{"Refresh", "Cache-Control", "Vary", "X-Robots-Tag", "Content-Type"} {
			got := strings.ReplaceAll(strings.Join(absent.Header().Values(name), ", "), missing, number)
			if want := strings.Join(existing.Header().Values(name), ", "); got != want {
				t.Errorf("%s: %s is %q for a missing order, %q for an existing one", route.name, name, got, want)
			}
		}
	}
}

func placeOwnedOrder(t *testing.T, s *cart.Store) (owner uuid.UUID, number string) {
	t.Helper()
	ctx := t.Context()
	if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"reload-"+uuid.NewString()+"@example.com").Scan(&owner); err != nil {
		t.Fatalf("create user: %v", err)
	}
	id := newCart(t, s)
	if err := s.Add(ctx, id, variantOf(t, "pixelight-9", true), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &order.Delivery{
		Email: "reload@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	number, err := placeOrder(t, s, ctx, id, uuid.NullUUID{UUID: owner, Valid: true},
		shipID, addr, "", "reload-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	return owner, number
}
