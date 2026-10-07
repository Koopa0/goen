package orderaccess_test

import (
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/orderaccess"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/ratelimit"
)

// TestOnlyARefusedCrossSiteNavigationIsLoadedAgain drives the two pages Stripe
// sends a customer back to. The stores are zero values, so reading either one
// panics: every answer here is decided before the order is read. With no
// cookie and nobody signed in the gate refuses without a query; the cookie
// columns are in the integration test.
func TestOnlyARefusedCrossSiteNavigationIsLoadedAgain(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	access := &orderaccess.Store{}
	orders := cart.NewHandler(&cart.Store{}, access, log, true, ratelimit.New(ratelimit.Config{
		Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000,
	}), nil, nil)
	payments := payment.NewHandler(&payment.Store{}, &payment.Gateway{}, access, log)

	const number = "GO-261007-000004"
	routes := []struct {
		name   string
		method string
		target string
		serve  http.HandlerFunc
	}{
		{"the order page Stripe returns to", http.MethodGet, "/orders/" + number + "?paid=1", orders.OrderPage},
		{"the pay page Stripe cancels to", http.MethodGet, "/orders/" + number + "/pay?cancelled=1", payments.Page},
	}
	for _, route := range routes {
		for _, tt := range []struct {
			fetchSite string // "" sends no Sec-Fetch-Site at all
			wantHop   bool
		}{
			{"cross-site", true},
			{"same-site", false},
			{"same-origin", false},
			{"none", false},
			{"", false},
		} {
			header := "Sec-Fetch-Site: " + tt.fetchSite
			if tt.fetchSite == "" {
				header = "no Sec-Fetch-Site"
			}
			t.Run(route.name+"/"+header, func(t *testing.T) {
				t.Parallel()
				res := serve(t, route.serve, route.method, route.target, number, tt.fetchSite)
				if tt.wantHop {
					assertLoadedAgain(t, res, route.target)
					return
				}
				assertRefused(t, res)
			})
		}
	}

	t.Run("a cross-site post to the pay page", func(t *testing.T) {
		t.Parallel()
		res := serve(t, payments.Start, http.MethodPost, "/orders/"+number+"/pay", number, "cross-site")
		assertRefused(t, res)
	})
}

// TestTheNextLoadIsTheRequestsOwnPathAndQuery holds the target to goen's own
// path, escaped as it arrived, and keeps the query a hostile link carries out
// of the markup and the header.
func TestTheNextLoadIsTheRequestsOwnPathAndQuery(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	payments := payment.NewHandler(&payment.Store{}, &payment.Gateway{}, &orderaccess.Store{}, log)

	for _, tt := range []struct {
		name, target, number, want string
	}{
		{"an absolute request target names another host", "http://evil.example/orders/GO-1/pay?paid=1", "GO-1", "/orders/GO-1/pay?paid=1"},
		{"an escaped slash stays escaped", "/orders/GO%2F1/pay", "GO/1", "/orders/GO%2F1/pay"},
		{"a query that would close the attribute", `/orders/GO-1/pay?x="><b>`, "GO-1", `/orders/GO-1/pay?x="><b>`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := serve(t, payments.Page, http.MethodGet, tt.target, tt.number, "cross-site")
			assertLoadedAgain(t, res, tt.want)
			if strings.Contains(res.Body.String(), "<b>") {
				t.Errorf("the query reached the markup unescaped:\n%s", res.Body.String())
			}
		})
	}
}

func serve(t *testing.T, h http.HandlerFunc, method, target, number, fetchSite string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody)
	req.SetPathValue("number", number)
	if fetchSite != "" {
		req.Header.Set("Sec-Fetch-Site", fetchSite)
	}
	res := httptest.NewRecorder()
	h(res, req)
	return res
}

func assertLoadedAgain(t *testing.T, res *httptest.ResponseRecorder, target string) {
	t.Helper()
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for the page that loads %s again", res.Code, target)
	}
	if got, want := res.Header().Get("Refresh"), "0; url="+target; got != want {
		t.Errorf("Refresh = %q, want %q", got, want)
	}
	body := res.Body.String()
	for _, want := range []string{
		`<meta http-equiv="refresh" content="0; url=` + html.EscapeString(target) + `">`,
		`href="` + html.EscapeString(target) + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %s:\n%s", want, body)
		}
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if !hasVary(res.Header(), "Sec-Fetch-Site") {
		t.Errorf("Vary = %q, want it to name Sec-Fetch-Site", res.Header().Values("Vary"))
	}
	if got := res.Header().Get("X-Robots-Tag"); got != "noindex" {
		t.Errorf("X-Robots-Tag = %q, want noindex", got)
	}
}

func assertRefused(t *testing.T, res *httptest.ResponseRecorder) {
	t.Helper()
	if res.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.Code)
	}
	if got := res.Header().Get("Refresh"); got != "" {
		t.Errorf("a refusal carries Refresh %q; this browser would be sent round again", got)
	}
	if strings.Contains(res.Body.String(), `http-equiv="refresh"`) {
		t.Error("a refusal carries a meta refresh; this browser would be sent round again")
	}
}

func hasVary(h http.Header, name string) bool {
	for _, line := range h.Values("Vary") {
		for token := range strings.SplitSeq(line, ",") {
			if strings.EqualFold(strings.TrimSpace(token), name) {
				return true
			}
		}
	}
	return false
}
