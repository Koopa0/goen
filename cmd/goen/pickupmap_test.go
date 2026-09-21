package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/payment"
)

// mapStagingOrigin is ECPay's staging logistics host, which is what an
// unconfigured GOEN_ECPAY_LOGISTICS_BASE_URL defaults to.
const mapStagingOrigin = "https://logistics-stage.ecpay.com.tw"

// storeMapRouter builds goen's REAL router — the one cmd/goen serves — with or
// without the store map configured.
//
// The pool is opened against an address nothing listens on, and never used: the
// routes these tests drive are the ones that reach no database, and the
// cross-origin refusal happens in middleware before any handler runs. pgxpool
// connects lazily, so this is a router with production wiring rather than a
// synthetic mux standing in for one.
func storeMapRouter(t *testing.T, configured bool) http.Handler {
	t.Helper()

	idle, err := pgxpool.New(t.Context(),
		"postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatalf("open an unused pool: %v", err)
	}
	t.Cleanup(idle.Close)

	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatalf("build a disabled payment gateway: %v", err)
	}

	mode := ""
	if configured {
		mode = string(cart.ModeB2C)
	}
	storeMap, err := cart.NewMap(testMerchantID, mode, "", "https://goen.test")
	if err != nil {
		t.Fatalf("build the store map: %v", err)
	}
	return newRouter(&RouterConfig{
		Pool: idle, AdminPool: idle, Payments: gateway,
		Refunder: admin.NewRefunder(""), BaseURL: "https://goen.test",
		StoreMap: storeMap,
	}, slog.New(slog.DiscardHandler))
}

// testMerchantID stands for whatever id the environment names. goen only
// compares it against what a callback repeats, so a made-up one will do.
const testMerchantID = "1000001"

// aStoreCallback is the form ECPay's page posts back, as it documents it.
func aStoreCallback() url.Values {
	return url.Values{
		"MerchantID":       {testMerchantID},
		"MerchantTradeNo":  {"ABCDEFGHIJ1234567890"},
		"LogisticsSubType": {"UNIMART"},
		"CVSStoreID":       {"131386"},
		"CVSStoreName":     {"南港園區"},
		"CVSAddress":       {"台北市南港區三重路19-2號"},
		"CVSOutSide":       {"0"},
		"ExtraData":        {"0123456789abcdef0123"},
	}
}

func crossSitePost(t *testing.T, path string, form url.Values) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path,
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// What a browser sends when one site's page posts to another's.
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Origin", "https://logistics-stage.ecpay.com.tw")
	return req
}

// TestTheBypassAdmitsExactlyOneMethodAndPath is the lock on goen's only hole in
// its cross-origin defence. It drives the real router, because the bypass is
// registered there and a synthetic mux would prove nothing about it.
func TestTheBypassAdmitsExactlyOneMethodAndPath(t *testing.T) {
	t.Parallel()
	router := storeMapRouter(t, true)

	t.Run("the return route is admitted and answers the interstitial", func(t *testing.T) {
		t.Parallel()
		res := httptest.NewRecorder()
		router.ServeHTTP(res, crossSitePost(t, cart.PickupReturnPath, aStoreCallback()))
		if res.Code != http.StatusOK {
			t.Fatalf("cross-site POST %s = %d, want 200", cart.PickupReturnPath, res.Code)
		}
		if got := res.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store: the body carries a store "+
				"a third party chose for whoever holds this browser", got)
		}
		if got := res.Header().Values("Set-Cookie"); len(got) > 0 {
			t.Errorf("the return response set %d cookies (%v); this route reads and "+
				"writes none, and the whole middleware chain must leave it that way",
				len(got), got)
		}
		body := res.Body.String()
		if !strings.Contains(body, `http-equiv="refresh"`) {
			t.Error("the interstitial does not refresh; a shopper with no scripting is stranded")
		}
		if !strings.Contains(body, "/checkout?") {
			t.Error("the refresh does not go to the checkout")
		}
	})

	t.Run("the trailing-slash variant is refused", func(t *testing.T) {
		t.Parallel()
		res := httptest.NewRecorder()
		router.ServeHTTP(res, crossSitePost(t, cart.PickupReturnPath+"/", aStoreCallback()))
		if res.Code != http.StatusForbidden {
			t.Errorf("cross-site POST %s/ = %d, want 403: the bypass matches the "+
				"pattern itself and never a path that would redirect to it",
				cart.PickupReturnPath, res.Code)
		}
	})

	t.Run("a cross-site checkout post is still refused", func(t *testing.T) {
		t.Parallel()
		res := httptest.NewRecorder()
		router.ServeHTTP(res, crossSitePost(t, "/checkout", url.Values{"shipping": {"x"}}))
		if res.Code != http.StatusForbidden {
			t.Errorf("cross-site POST /checkout = %d, want 403", res.Code)
		}
	})

	t.Run("a malformed callback is refused by shape, not by the defence", func(t *testing.T) {
		t.Parallel()
		form := aStoreCallback()
		form.Set("MerchantID", "1000002")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, crossSitePost(t, cart.PickupReturnPath, form))
		if res.Code != http.StatusBadRequest {
			t.Errorf("a callback naming another merchant = %d, want 400", res.Code)
		}
		if strings.Contains(res.Body.String(), "1000002") {
			t.Error("the refusal repeats what it was posted")
		}
	})
}

// TestAnUnconfiguredGoenHasNoReturnRouteAndNoBypass is the other half: with no
// carrier configured, nothing about main's behaviour moved.
func TestAnUnconfiguredGoenHasNoReturnRouteAndNoBypass(t *testing.T) {
	t.Parallel()
	router := storeMapRouter(t, false)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, crossSitePost(t, cart.PickupReturnPath, aStoreCallback()))
	if res.Code != http.StatusForbidden {
		t.Errorf("cross-site POST %s = %d on an unconfigured goen, want 403: "+
			"the bypass exists only where a carrier does", cart.PickupReturnPath, res.Code)
	}

	sameSite := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		cart.PickupReturnPath, strings.NewReader(aStoreCallback().Encode()))
	sameSite.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	sameSite.Header.Set("Sec-Fetch-Site", "same-origin")
	res = httptest.NewRecorder()
	router.ServeHTTP(res, sameSite)
	// 405 and not 404: no POST handler is registered for this path at all, so
	// the only pattern its path matches is the catalogue's GET catch-all.
	if res.Code != http.StatusMethodNotAllowed {
		t.Errorf("same-site POST %s = %d on an unconfigured goen, want 405: "+
			"the route is not registered at all", cart.PickupReturnPath, res.Code)
	}
	if got := res.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
		t.Errorf("the policy of an unconfigured goen is\n  %q\nand main's is\n  %q\n"+
			"they must be byte-identical", got, contentSecurityPolicy)
	}
}

// TestThePolicyNamesTheMapOriginOnlyWhenConfigured holds R5: form-action gains
// exactly one origin, as a DESTINATION, and nothing else about the policy moves.
func TestThePolicyNamesTheMapOriginOnlyWhenConfigured(t *testing.T) {
	t.Parallel()

	if got := policyWith(""); got != contentSecurityPolicy {
		t.Fatalf("policyWith(\"\") is not the policy main sends:\n  %q\n  %q",
			got, contentSecurityPolicy)
	}

	widened := policyWith(mapStagingOrigin)
	before := directivesOf(t, contentSecurityPolicy)
	after := directivesOf(t, widened)
	if len(before) != len(after) {
		t.Fatalf("the widened policy has %d directives, the plain one %d", len(after), len(before))
	}
	for name, plain := range before {
		if name == "form-action" {
			continue
		}
		if after[name] != plain {
			t.Errorf("%s changed: %q became %q", name, plain, after[name])
		}
	}
	formAction := after["form-action"]
	if !strings.Contains(formAction, mapStagingOrigin) {
		t.Errorf("form-action is %q and does not name the map, so the button is "+
			"blocked by the browser", formAction)
	}
	if !strings.Contains(formAction, "'self'") ||
		!strings.Contains(formAction, "https://checkout.stripe.com") {
		t.Errorf("form-action is %q; widening it dropped a destination goen needs", formAction)
	}
	if n := strings.Count(formAction, "https://logistics"); n != 1 {
		t.Errorf("form-action names %d logistics origins, want exactly 1: staging OR "+
			"production, never both", n)
	}
}

// directivesOf splits a policy into name -> value.
func directivesOf(t *testing.T, policy string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, d := range strings.Split(policy, ";") {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		name, value, _ := strings.Cut(d, " ")
		out[name] = value
	}
	return out
}

// TestTheCheckoutStillTellsTheCarrierNothingAboutItself pins the header that
// keeps the checkout's own URL — which now carries a store and a nonce — out of
// the Referer the map request sends.
func TestTheCheckoutStillTellsTheCarrierNothingAboutItself(t *testing.T) {
	t.Parallel()
	router := storeMapRouter(t, true)

	// No cart cookie, so this answers 303 to /cart without touching a database.
	// The headers under test are set before any handler runs.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/checkout", http.NoBody)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if got := res.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Errorf("Referrer-Policy on the checkout = %q; anything looser sends the "+
			"checkout URL, nonce and store included, to the carrier", got)
	}
	if got := res.Header().Get("Content-Security-Policy"); !strings.Contains(got, mapStagingOrigin) {
		t.Errorf("the configured policy does not name the map: %q", got)
	}
}

// TestTheReturnRouteRendersNoChrome pins the routing decision that keeps an
// anonymous cross-site POST from costing a category query.
func TestTheReturnRouteRendersNoChrome(t *testing.T) {
	t.Parallel()

	if navPath(cart.PickupReturnPath) {
		t.Errorf("%s is a nav path, so withTopNav runs store.Nav() for every "+
			"anonymous cross-site post to it", cart.PickupReturnPath)
	}
	if storefrontPath(cart.PickupReturnPath) {
		t.Errorf("%s is a storefront path, so withBanner would read a promotion "+
			"for a page that renders none", cart.PickupReturnPath)
	}
}
