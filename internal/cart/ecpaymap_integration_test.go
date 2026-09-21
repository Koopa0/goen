//go:build integration

package cart_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
)

// mapSandboxMerchant is ECPay's own published B2C staging merchant, which is
// public documentation rather than a credential.
const mapSandboxMerchant = "2000132"

// aForeignNonce is a well-formed nonce that no browser here holds.
const aForeignNonce = "fedcba98765432100000"

func configuredMap(t *testing.T) *cart.Map {
	t.Helper()
	m, err := cart.NewMap(mapSandboxMerchant, string(cart.ModeB2C), "", "https://goen.test")
	if err != nil {
		t.Fatalf("build the store map: %v", err)
	}
	return m
}

// aPickupCart is a cart holding one sellable thing, with the pickup method's
// version id and the browser's own cart cookie.
func aPickupCart(t *testing.T, s *cart.Store, label string) (token string, shipping uuid.UUID) {
	t.Helper()
	tok, err := cart.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	cartID, err := s.Create(t.Context(), tok, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if err := s.Add(t.Context(), cartID, freshVariant(t, label), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	return tok, shipVersionFor(t, "store_pickup")
}

// cookieNamed picks one Set-Cookie out of a response.
func cookieNamed(res *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range res.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// openTheCheckout renders the checkout the way a browser reaches it: a GET,
// carrying whichever cookies this browser holds.
func openTheCheckout(
	t *testing.T, h *cart.Handler, token, query string, cookies ...*http.Cookie,
) (body string, status int) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/checkout"+query, http.NoBody)
	//nolint:gosec // G124: the browser's own cart cookie
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
	for _, c := range cookies {
		req.AddCookie(c)
	}
	res := httptest.NewRecorder()
	h.Checkout(res, req)
	return res.Body.String(), res.Code
}

// theMapAnswers is the callback ECPay's page posts back, driven through the
// return handler exactly as the shopper's own browser would.
func theMapAnswers(t *testing.T, h *cart.Handler, nonce, code, name, address string) (
	target string, status int, headers http.Header,
) {
	t.Helper()
	form := url.Values{
		"MerchantID":       {mapSandboxMerchant},
		"MerchantTradeNo":  {"ABCDEFGHIJ1234567890"},
		"LogisticsSubType": {"UNIMART"},
		"CVSStoreID":       {code},
		"CVSStoreName":     {name},
		"CVSAddress":       {address},
		"CVSOutSide":       {"0"},
		"ExtraData":        {nonce},
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		cart.PickupReturnPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// The browser is coming from ECPay's page, so it sends neither cookie.
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	res := httptest.NewRecorder()
	h.PickupReturn(res, req)
	return refreshTargetOf(res.Body.String()), res.Code, res.Header()
}

// refreshTargetOf reads the URL out of the interstitial's meta refresh.
func refreshTargetOf(body string) string {
	const marker = `content="0; url=`
	i := strings.Index(body, marker)
	if i < 0 {
		return ""
	}
	rest := body[i+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	// templ escapes the attribute; the browser unescapes it before navigating.
	return strings.NewReplacer("&amp;", "&", "&#34;", `"`, "&#39;", "'").Replace(rest[:end])
}

// openPickupCheckout renders the checkout the way the chain chooser does: a
// POST that applies 7-ELEVEN and a mobile-carrier invoice and validates
// nothing. The chain has to be applied before the map form exists, because that
// form is built for one chain on the server and a sibling form cannot read a
// radio nobody has applied yet.
func openPickupCheckout(
	t *testing.T, h *cart.Handler, token string, shipping uuid.UUID,
	cookies ...*http.Cookie,
) (body string, pickupCookie *http.Cookie, status int) {
	t.Helper()
	form := url.Values{
		"shipping": {shipping.String()}, "pickup_brand": {"seven_eleven"},
		"invoice_type": {"mobile_carrier"}, "update": {"pickup_brand"},
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	//nolint:gosec // G124: the browser's own cart cookie
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
	for _, c := range cookies {
		req.AddCookie(c)
	}
	res := httptest.NewRecorder()
	h.PlaceOrder(res, req)
	return res.Body.String(), cookieNamed(res, "goen_pickup"), res.Code
}

// TestAStoreChosenOnTheMapSurvivesTheRoundTripAndReachesTheOrder is the happy
// path end to end, through the three requests a real browser makes.
func TestAStoreChosenOnTheMapSurvivesTheRoundTripAndReachesTheOrder(t *testing.T) {
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false, testLimiter(), nil, configuredMap(t))
	token, shipping := aPickupCart(t, s, "map-roundtrip")

	// 1. The checkout, with a chain chosen so the map form is built for it.
	page, cookie, status := openPickupCheckout(t, h, token, shipping)
	if status != http.StatusOK {
		t.Fatalf("checkout = %d, want 200", status)
	}
	if cookie == nil {
		t.Fatal("the checkout issued no pickup cookie, so nothing can vouch for a store")
	}
	nonce, found := hiddenInputValue(page, "ExtraData")
	if !found {
		t.Fatal("the map form carries no ExtraData, so no nonce reaches ECPay")
	}
	if !strings.Contains(page, `value="UNIMART"`) {
		t.Error("the map form does not name 7-ELEVEN's subtype under the b2c contract")
	}
	firstTradeNo, _ := hiddenInputValue(page, "MerchantTradeNo")

	// A second render keeps the nonce and takes a fresh correlation number.
	second, secondCookie, _ := openPickupCheckout(t, h, token, shipping, cookie)
	if secondNonce, _ := hiddenInputValue(second, "ExtraData"); secondNonce != nonce {
		t.Errorf("the nonce changed between renders (%q then %q); reload and the "+
			"back button would each lose the store", nonce, secondNonce)
	}
	if secondTradeNo, _ := hiddenInputValue(second, "MerchantTradeNo"); secondTradeNo == firstTradeNo {
		t.Error("two renders reused one MerchantTradeNo, which ECPay requires unique per call")
	}
	if secondCookie == nil {
		t.Error("the second render did not rewrite the cookie, so an invoice choice " +
			"made after the first one would come back stale")
	}

	// 2. The carrier's page posts the store back, cross-site.
	target, status, headers := theMapAnswers(t, h, nonce, "131386", "南港園區", "台北市南港區三重路19-2號")
	if status != http.StatusOK {
		t.Fatalf("the return handler = %d, want 200", status)
	}
	if len(headers.Values("Set-Cookie")) != 0 {
		t.Errorf("the return set %v; it reads and writes no cookie", headers.Values("Set-Cookie"))
	}
	if target == "" {
		t.Fatal("the interstitial names no target")
	}

	// 3. The refreshed GET, carrying the Lax pickup cookie and no query of its own.
	page, status = openTheCheckout(t, h, token, target[len("/checkout"):], cookie)
	if status != http.StatusOK {
		t.Fatalf("the checkout after the return = %d, want 200; body=%s", status, page)
	}
	for _, want := range []string{"南港園區", "131386", "台北市南港區三重路19-2號"} {
		if !strings.Contains(page, want) {
			t.Errorf("the checkout does not show %q, which is what the shopper reads "+
				"before paying and the only check there is on a substituted store", want)
		}
	}
	// R2: the two choices made before the trip came back with it.
	if !strings.Contains(page, `value="`+shipping.String()+`"`) {
		t.Error("the delivery method did not survive the round trip")
	}
	if !strings.Contains(page, `name="invoice_type" value="mobile_carrier" checked`) {
		t.Error("the 發票 choice did not survive the round trip")
	}

	// 4. Placing the order carries the store into order_private_data.
	number := placeThisCheckout(t, h, token, cookie, page, shipping, "map-roundtrip")
	street, brand, code, name := destinationOf(t, number)
	if street != "" {
		t.Errorf("a pickup order recorded a street address %q", street)
	}
	if brand != "seven_eleven" || code != "131386" || name != "南港園區" {
		t.Errorf("order destination = %q/%q/%q, want seven_eleven/131386/南港園區",
			brand, code, name)
	}
}

// placeThisCheckout submits the rendered checkout the way its own form would,
// and returns the order number it was redirected to.
func placeThisCheckout(
	t *testing.T, h *cart.Handler, token string, pickupCookie *http.Cookie,
	page string, shipping uuid.UUID, label string,
) string {
	t.Helper()
	quote, ok := hiddenInputValue(page, "checkout_quote")
	if !ok {
		t.Fatal("the rendered checkout carries no quote")
	}
	idempotency, ok := hiddenInputValue(page, "idempotency")
	if !ok {
		t.Fatal("the rendered checkout carries no attempt identity")
	}
	form := url.Values{
		"email": {label + "@example.com"}, "name": {"王小明"}, "phone": {"0912345678"},
		"shipping": {shipping.String()}, "pickup_brand": {"seven_eleven"},
		"invoice_type":   {"mobile_carrier"},
		"checkout_quote": {quote}, "idempotency": {idempotency},
	}
	form.Set("invoice_carrier", "/ABC+123")
	for _, name := range []string{"pickup_store_code", "pickup_store_name", "pickup_store_brand", "pickup_n"} {
		if v, found := hiddenInputValue(page, name); found {
			form.Set(name, v)
		}
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	//nolint:gosec // G124: the browser's own cart cookie
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
	if pickupCookie != nil {
		req.AddCookie(pickupCookie)
	}
	res := httptest.NewRecorder()
	h.PlaceOrder(res, req)

	if res.Code != http.StatusSeeOther {
		t.Fatalf("placing the order = %d, want 303; body=%s", res.Code, res.Body.String())
	}
	if cleared := cookieNamed(res, "goen_pickup"); cleared == nil || cleared.MaxAge >= 0 {
		t.Error("the pickup cookie outlived the order; its nonce would vouch for a " +
			"store in the next checkout this browser starts")
	}
	location := res.Header().Get("Location")
	number := strings.TrimSuffix(strings.TrimPrefix(location, "/orders/"), "/pay")
	if number == "" || number == location {
		t.Fatalf("cannot read an order number out of %q", location)
	}
	return number
}

// TestAForgedStorePostedFromAnotherSiteEndsWithNoStore is T1. An attacker page
// auto-posts a store to the return URL in the victim's browser. It cannot know
// the nonce, so the checkout drops the store and says so without repeating it.
func TestAForgedStorePostedFromAnotherSiteEndsWithNoStore(t *testing.T) {
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false, testLimiter(), nil, configuredMap(t))
	token, shipping := aPickupCart(t, s, "map-forged")

	_, cookie, _ := openPickupCheckout(t, h, token, shipping)
	if cookie == nil {
		t.Fatal("no pickup cookie; the refusal below would prove nothing")
	}

	target, status, _ := theMapAnswers(t, h, aForeignNonce, "999999", "攻擊者的門市", "攻擊者的地址")
	if status != http.StatusOK {
		t.Fatalf("the return handler = %d; it validates shape only and this shape is "+
			"valid — the refusal belongs at the checkout", status)
	}

	page, code := openTheCheckout(t, h, token, target[len("/checkout"):], cookie)
	if code != http.StatusUnprocessableEntity {
		t.Errorf("the checkout answered %d for a store this browser never fetched, want 422", code)
	}
	for _, forged := range []string{"999999", "攻擊者的門市", "攻擊者的地址"} {
		if strings.Contains(page, forged) {
			t.Errorf("the checkout shows %q, which an attacker chose", forged)
		}
	}
	if strings.Contains(page, `name="pickup_store_code"`) {
		t.Error("the forged store is carried in a hidden field, so the next submit places it")
	}
}

// TestACraftedCheckoutLinkEndsWithNoStore is T2: a link sent to the victim,
// carrying a store and no nonce at all.
func TestACraftedCheckoutLinkEndsWithNoStore(t *testing.T) {
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false, testLimiter(), nil, configuredMap(t))
	token, shipping := aPickupCart(t, s, "map-crafted")

	_, cookie, _ := openPickupCheckout(t, h, token, shipping)
	if cookie == nil {
		t.Fatal("no pickup cookie; the refusal below would prove nothing")
	}

	crafted := "?" + url.Values{
		"ship":              {shipping.String()},
		"pickup_brand":      {"seven_eleven"},
		"pickup_store_code": {"999999"},
		"pickup_store_name": {"攻擊者的門市"},
		"pickup_store_addr": {"攻擊者的地址"},
	}.Encode()
	page, code := openTheCheckout(t, h, token, crafted, cookie)
	if code != http.StatusUnprocessableEntity {
		t.Errorf("a crafted link answered %d, want 422", code)
	}
	for _, forged := range []string{"999999", "攻擊者的門市", "攻擊者的地址"} {
		if strings.Contains(page, forged) {
			t.Errorf("the checkout shows %q from a link somebody was sent", forged)
		}
	}
}

// TestPlacingAnOrderWithAnUnvouchedStoreDropsIt is the placement half of the
// same rule, which is what stops a 422 re-render from carrying a dropped store
// back in its own hidden inputs.
func TestPlacingAnOrderWithAnUnvouchedStoreDropsIt(t *testing.T) {
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false, testLimiter(), nil, configuredMap(t))
	token, shipping := aPickupCart(t, s, "map-placement")

	page, cookie, _ := openPickupCheckout(t, h, token, shipping)
	quote, _ := hiddenInputValue(page, "checkout_quote")
	idempotency, _ := hiddenInputValue(page, "idempotency")

	for _, tt := range []struct {
		name  string
		nonce string
	}{
		{name: "no nonce at all"},
		{name: "a nonce of the attacker's own", nonce: aForeignNonce},
	} {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{
				"email": {"drop@example.com"}, "name": {"王小明"}, "phone": {"0912345678"},
				"shipping": {shipping.String()}, "pickup_brand": {"seven_eleven"},
				"pickup_store_code": {"999999"}, "pickup_store_name": {"攻擊者的門市"},
				"pickup_store_brand": {"seven_eleven"},
				"pickup_n":           {tt.nonce},
				"invoice_type":       {"mobile_carrier"},
				"invoice_carrier":    {"/ABC+123"},
				"checkout_quote":     {quote}, "idempotency": {idempotency},
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout",
				strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			//nolint:gosec // G124: the browser's own cart cookie
			req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
			req.AddCookie(cookie)
			res := httptest.NewRecorder()
			h.PlaceOrder(res, req)

			if res.Code != http.StatusUnprocessableEntity {
				t.Fatalf("placing an order with an unvouched store = %d, want 422", res.Code)
			}
			body := res.Body.String()
			for _, forged := range []string{"999999", "攻擊者的門市"} {
				if strings.Contains(body, forged) {
					t.Errorf("the 422 carries %q back, so the next submit places it", forged)
				}
			}
		})
	}
}

// TestAnUnconfiguredCheckoutIsExactlyWhatItWas is the last line of the design:
// with no carrier, the checkout asks for a chain, issues no cookie, and places
// the order it always placed.
func TestAnUnconfiguredCheckoutIsExactlyWhatItWas(t *testing.T) {
	s := cart.NewStore(pool)
	off, err := cart.NewMap("", "", "", "https://goen.test")
	if err != nil {
		t.Fatalf("build a disabled map: %v", err)
	}
	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false, testLimiter(), nil, off)
	token, shipping := aPickupCart(t, s, "map-unconfigured")

	page, cookie, status := openPickupCheckout(t, h, token, shipping)
	if status != http.StatusOK {
		t.Fatalf("checkout = %d, want 200", status)
	}
	if cookie != nil {
		t.Error("an unconfigured goen issued a pickup cookie")
	}
	for _, gone := range []string{"pickup-map-form", "ecpay.com.tw"} {
		if strings.Contains(page, gone) {
			t.Errorf("an unconfigured checkout renders %q", gone)
		}
	}

	number := placeThisCheckout(t, h, token, nil, page, shipping, "map-unconfigured")
	_, brand, code, name := destinationOf(t, number)
	if brand != "seven_eleven" {
		t.Errorf("chain = %q, want seven_eleven", brand)
	}
	if code != "" || name != "" {
		t.Errorf("an unconfigured checkout stored a store (%q/%q); nobody can choose one", code, name)
	}
}

// TestChangingTheChainDropsTheOtherChainsStore holds rule 9: the store belongs
// to the chain it was chosen at, and a shopper who changes chain has no store.
func TestChangingTheChainDropsTheOtherChainsStore(t *testing.T) {
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false, testLimiter(), nil, configuredMap(t))
	token, shipping := aPickupCart(t, s, "map-chain-change")

	page, cookie, _ := openPickupCheckout(t, h, token, shipping)
	nonce, _ := hiddenInputValue(page, "ExtraData")
	target, _, _ := theMapAnswers(t, h, nonce, "131386", "南港園區", "台北市南港區三重路19-2號")
	page, _ = openTheCheckout(t, h, token, target[len("/checkout"):], cookie)
	if !strings.Contains(page, "南港園區") {
		t.Fatal("the store was never honoured, so this test proves nothing")
	}

	// The chain chooser re-renders the form; the store still names 7-ELEVEN.
	form := url.Values{
		"email": {"chain@example.com"}, "name": {"王小明"}, "phone": {"0912345678"},
		"shipping": {shipping.String()}, "pickup_brand": {"family_mart"},
		"update": {"pickup_brand"},
	}
	for _, field := range []string{"pickup_store_code", "pickup_store_name", "pickup_store_brand", "pickup_n"} {
		if v, found := hiddenInputValue(page, field); found {
			form.Set(field, v)
		}
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	//nolint:gosec // G124: the browser's own cart cookie
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	h.PlaceOrder(res, req)

	if strings.Contains(res.Body.String(), "南港園區") {
		t.Error("a 7-ELEVEN store survived a change to 全家; the parcel would wait " +
			"at a store of the wrong chain")
	}
	if !strings.Contains(res.Body.String(), `value="FAMI"`) {
		t.Error("the map form was not rebuilt for the chain the shopper just chose")
	}
}
