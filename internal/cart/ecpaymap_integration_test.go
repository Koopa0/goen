//go:build integration

package cart_test

import (
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/orderaccess"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/user"
)

// mapMerchantID stands for whatever id the environment names; goen only ever
// compares it against what the callback repeats.
const mapMerchantID = "1000001"

// aForeignNonce is a well-formed nonce that no browser here holds.
const aForeignNonce = "fedcba98765432100000"

func configuredMap(t *testing.T) *cart.StoreMap {
	t.Helper()
	m, err := cart.NewStoreMap(mapMerchantID, string(cart.ModeB2C), "", "https://goen.test")
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

// pickupCookieOf picks the pickup cookie out of a response.
func pickupCookieOf(res *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range res.Result().Cookies() {
		if c.Name == "goen_pickup" {
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
		"MerchantID":       {mapMerchantID},
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

// startPickup is 「選擇門市」: the checkout form posted to the start route, which
// keeps what was typed and answers 303 to the GET hand-off page holding the
// carrier's form. It returns that page, as the browser follows it, and the
// pickup cookie the start issued. A start that sends the shopper back to the
// checkout instead returns that 303 and no body.
func startPickup(
	t *testing.T, h *cart.Handler, token string, fields url.Values, cookies ...*http.Cookie,
) (body string, pickupCookie *http.Cookie, status int) {
	t.Helper()
	return startPickupAs(t, h, token, nil, fields, cookies...)
}

// startPickupAs is startPickup for a signed-in member: a member-owned cart is
// only served to its owner, so the start and the hand-off page both carry who.
func startPickupAs(
	t *testing.T, h *cart.Handler, token string, who *user.User, fields url.Values, cookies ...*http.Cookie,
) (body string, pickupCookie *http.Cookie, status int) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, pages.PickupStartAction,
		strings.NewReader(fields.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	//nolint:gosec // G124: the browser's own cart cookie
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
	for _, c := range cookies {
		req.AddCookie(c)
	}
	if who != nil {
		req = req.WithContext(user.NewContext(req.Context(), *who))
	}
	res := httptest.NewRecorder()
	h.PickupStart(res, req)
	pickupCookie = pickupCookieOf(res)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != pages.PickupMapPath {
		return res.Body.String(), pickupCookie, res.Code
	}

	next := httptest.NewRequestWithContext(t.Context(), http.MethodGet, pages.PickupMapPath, http.NoBody)
	//nolint:gosec // G124: the browser's own cart cookie
	next.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
	if pickupCookie != nil {
		next.AddCookie(pickupCookie)
	}
	if who != nil {
		next = next.WithContext(user.NewContext(next.Context(), *who))
	}
	page := httptest.NewRecorder()
	h.PickupMap(page, next)
	return page.Body.String(), pickupCookie, page.Code
}

// aStart is the fields 「選擇門市」 carries when nothing but the choices has been
// made: the method, the chain and the invoice type.
func aStart(shipping uuid.UUID) url.Values {
	return url.Values{
		"shipping": {shipping.String()}, "pickup_chain": {"seven_eleven"},
		"invoice_type": {"mobile_carrier"},
	}
}

// openPickupCheckout renders the checkout the way the chain chooser does: a
// POST that applies 7-ELEVEN and a mobile-carrier invoice and validates
// nothing. It is the page the shopper reads; the nonce and the carrier's form
// come from startPickup.
func openPickupCheckout(
	t *testing.T, h *cart.Handler, token string, shipping uuid.UUID,
) (body string, pickupCookie *http.Cookie, status int) {
	t.Helper()
	form := url.Values{
		"shipping": {shipping.String()}, "pickup_chain": {"seven_eleven"},
		"invoice_type": {"mobile_carrier"}, "update": {"pickup_chain"},
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	//nolint:gosec // G124: the browser's own cart cookie
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
	res := httptest.NewRecorder()
	h.PlaceOrder(res, req)
	return res.Body.String(), pickupCookieOf(res), res.Code
}

// TestAStoreChosenOnTheMapSurvivesTheRoundTripAndReachesTheOrder is the happy
// path end to end, through the three requests a real browser makes.
func TestAStoreChosenOnTheMapSurvivesTheRoundTripAndReachesTheOrder(t *testing.T) {
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler), false, testLimiter(), nil, configuredMap(t))
	token, shipping := aPickupCart(t, s, "map-roundtrip")

	// 1. 「選擇門市」: the form is posted to goen, which opens the map for the chain.
	page, cookie, status := startPickup(t, h, token, aStart(shipping))
	if status != http.StatusOK {
		t.Fatalf("the store-map start = %d, want 200", status)
	}
	if cookie == nil {
		t.Fatal("the start issued no pickup cookie, so nothing can vouch for a store")
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
	second, secondCookie, _ := startPickup(t, h, token, aStart(shipping), cookie)
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
	street, chain, code, name := destinationOf(t, number)
	if street != "" {
		t.Errorf("a pickup order recorded a street address %q", street)
	}
	if chain != "seven_eleven" || code != "131386" || name != "南港園區" {
		t.Errorf("order destination = %q/%q/%q, want seven_eleven/131386/南港園區",
			chain, code, name)
	}
}

// placeThisCheckout submits the rendered checkout the way its own form would,
// and returns the order number it was redirected to.
func placeThisCheckout(
	t *testing.T, h *cart.Handler, token string, pickupCookie *http.Cookie,
	page string, shipping uuid.UUID, label string, extraCookies ...*http.Cookie,
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
		"shipping": {shipping.String()}, "pickup_chain": {"seven_eleven"},
		"invoice_type":   {"mobile_carrier"},
		"checkout_quote": {quote}, "idempotency": {idempotency},
	}
	form.Set("invoice_carrier", "/ABC+123")
	for _, name := range []string{"pickup_store_code", "pickup_store_name", "pickup_store_chain", "pickup_n"} {
		if v, found := hiddenInputValue(page, name); found {
			form.Set(name, v)
		}
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	//nolint:gosec // G124: the browser's own cart cookie
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
	for _, cookie := range extraCookies {
		req.AddCookie(cookie)
	}
	if pickupCookie != nil {
		req.AddCookie(pickupCookie)
	}
	res := httptest.NewRecorder()
	h.PlaceOrder(res, req)

	if res.Code != http.StatusSeeOther {
		t.Fatalf("placing the order = %d, want 303; body=%s", res.Code, res.Body.String())
	}
	if cleared := pickupCookieOf(res); cleared == nil || cleared.MaxAge >= 0 {
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
	h := cart.NewHandler(s, orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler), false, testLimiter(), nil, configuredMap(t))
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
	if !strings.Contains(page, html.EscapeString(i18n.T(t.Context(), i18n.KeyPickupStoreUnconfirmed))) {
		t.Error("the refusal does not say the store could not be confirmed")
	}
	if !strings.Contains(page, `name="invoice_type" value="mobile_carrier" checked`) &&
		!strings.Contains(page, `value="mobile_carrier" checked`) {
		t.Error("the refusal lost the invoice choice the shopper had made")
	}
}

// TestACraftedCheckoutLinkEndsWithNoStore is T2: a link sent to the victim,
// carrying a store and no nonce at all.
func TestACraftedCheckoutLinkEndsWithNoStore(t *testing.T) {
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler), false, testLimiter(), nil, configuredMap(t))
	token, shipping := aPickupCart(t, s, "map-crafted")

	_, cookie, _ := openPickupCheckout(t, h, token, shipping)
	if cookie == nil {
		t.Fatal("no pickup cookie; the refusal below would prove nothing")
	}

	crafted := "?" + url.Values{
		"ship":              {shipping.String()},
		"pickup_chain":      {"seven_eleven"},
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
	h := cart.NewHandler(s, orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler), false, testLimiter(), nil, configuredMap(t))
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
				"shipping": {shipping.String()}, "pickup_chain": {"seven_eleven"},
				"pickup_store_code": {"999999"}, "pickup_store_name": {"攻擊者的門市"},
				"pickup_store_chain": {"seven_eleven"},
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

// postPickupCheckout submits a pickup checkout with exactly the fields given,
// which is what a forged POST is: the form's own quote and attempt identity and
// nothing the map vouched for.
func postPickupCheckout(
	t *testing.T, h *cart.Handler, token, page string, shipping uuid.UUID, label string, fields url.Values,
) (status int, body string) {
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
		"shipping": {shipping.String()}, "invoice_type": {"mobile_carrier"},
		"invoice_carrier": {"/ABC+123"}, "checkout_quote": {quote}, "idempotency": {idempotency},
	}
	for k, v := range fields {
		form[k] = v
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	//nolint:gosec // G124: the browser's own cart cookie
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
	res := httptest.NewRecorder()
	h.PlaceOrder(res, req)
	return res.Code, res.Body.String()
}

// TestPickupIsOfferedOnlyWhereTheStoreMapIsConfigured holds that checkout does
// not offer a method that can only be refused.
func TestPickupIsOfferedOnlyWhereTheStoreMapIsConfigured(t *testing.T) {
	disabled, err := cart.NewStoreMap("", "", "", "https://goen.test")
	if err != nil {
		t.Fatalf("build a disabled map: %v", err)
	}
	for name, tt := range map[string]struct {
		storeMap *cart.StoreMap
		offered  bool
	}{"map enabled": {configuredMap(t), true}, "map disabled": {disabled, false}} {
		t.Run(name, func(t *testing.T) {
			s := cart.NewStore(pool)
			h := cart.NewHandler(s, orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler), false, testLimiter(), nil, tt.storeMap)
			token, shipping := aPickupCart(t, s, "pickup-offer")
			page, _, status := openPickupCheckout(t, h, token, shipping)
			if status != http.StatusOK {
				t.Fatalf("checkout = %d, want 200", status)
			}
			if got := strings.Contains(page, `value="`+shipping.String()+`"`); got != tt.offered {
				t.Errorf("pickup radio present = %v, want %v", got, tt.offered)
			}
			if !strings.Contains(page, `name="shipping"`) {
				t.Error("no delivery choice is offered at all")
			}
		})
	}
}

// TestAForgedPickupPostIsRefusedWithOrWithoutTheMap holds that a checkout takes
// only 7-ELEVEN and 全家, and only with a store chosen on the carrier's map: a
// hand-written POST naming another chain, or any chain and no store, is a 422
// whether or not this deployment can open the map.
func TestAForgedPickupPostIsRefusedWithOrWithoutTheMap(t *testing.T) {
	disabled, err := cart.NewStoreMap("", "", "", "https://goen.test")
	if err != nil {
		t.Fatalf("build a disabled map: %v", err)
	}
	for name, storeMap := range map[string]*cart.StoreMap{"map enabled": configuredMap(t), "map disabled": disabled} {
		for _, forged := range []struct {
			name   string
			fields url.Values
			field  string
		}{
			{"hi_life", url.Values{"pickup_chain": {"hi_life"}, "pickup_store_code": {"999999"}, "pickup_store_name": {"攻擊者的門市"}}, "pickup_chain"},
			{"ok_mart", url.Values{"pickup_chain": {"ok_mart"}}, "pickup_chain"},
			{"no store", url.Values{"pickup_chain": {"seven_eleven"}}, "pickup_store"},
			{"typed store", url.Values{"pickup_chain": {"family_mart"}, "pickup_store_code": {"999999"}, "pickup_store_name": {"攻擊者的門市"}}, "pickup_store"},
		} {
			t.Run(name+"/"+forged.name, func(t *testing.T) {
				s := cart.NewStore(pool)
				h := cart.NewHandler(s, orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler), false, testLimiter(), nil, storeMap)
				token, shipping := aPickupCart(t, s, "forged-pickup")
				page, _, status := openPickupCheckout(t, h, token, shipping)
				if status != http.StatusOK {
					t.Fatalf("checkout = %d, want 200", status)
				}
				code, body := postPickupCheckout(t, h, token, page, shipping, "forged-pickup", forged.fields)
				if code != http.StatusUnprocessableEntity {
					t.Fatalf("forged pickup POST = %d, want 422", code)
				}
				if strings.Contains(body, "攻擊者的門市") {
					t.Error("the refusal echoes the forged store")
				}
				// Without a map the method is not offered, so the refusal is of
				// the method itself; with one, of the chain or the store.
				want := forged.field
				if !storeMap.Enabled() {
					want = "shipping"
				}
				if !strings.Contains(body, `id="`+want+`-error"`) {
					t.Errorf("the refusal must show the %s error", want)
				}
			})
		}
	}
}

// TestChangingTheChainDropsTheOtherChainsStore holds rule 9: the store belongs
// to the chain it was chosen at, and a shopper who changes chain has no store.
func TestChangingTheChainDropsTheOtherChainsStore(t *testing.T) {
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler), false, testLimiter(), nil, configuredMap(t))
	token, shipping := aPickupCart(t, s, "map-chain-change")

	start, cookie, _ := startPickup(t, h, token, aStart(shipping))
	nonce, _ := hiddenInputValue(start, "ExtraData")
	target, _, _ := theMapAnswers(t, h, nonce, "131386", "南港園區", "台北市南港區三重路19-2號")
	page, _ := openTheCheckout(t, h, token, target[len("/checkout"):], cookie)
	if !strings.Contains(page, "南港園區") {
		t.Fatal("the store was never honoured, so this test proves nothing")
	}

	// The chain chooser re-renders the form; the store still names 7-ELEVEN.
	form := url.Values{
		"email": {"chain@example.com"}, "name": {"王小明"}, "phone": {"0912345678"},
		"shipping": {shipping.String()}, "pickup_chain": {"family_mart"},
		"update": {"pickup_chain"},
	}
	for _, field := range []string{"pickup_store_code", "pickup_store_name", "pickup_store_chain", "pickup_n"} {
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
	// The map is opened for the chain posted with the button, never for a stale one.
	fields := aStart(shipping)
	fields.Set("pickup_chain", "family_mart")
	again, _, _ := startPickup(t, h, token, fields, cookie)
	if !strings.Contains(again, `value="FAMI"`) || strings.Contains(again, `value="UNIMART"`) {
		t.Error("the map was not opened for the chain the shopper just chose")
	}
}

func TestDuplicatePickupCookiesKeepTheReturnedStoreThroughPlacement(t *testing.T) {
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler), false, testLimiter(), nil, configuredMap(t))
	token, shipping := aPickupCart(t, s, "map-duplicate-cookie")
	_, stale, _ := startPickup(t, h, token, aStart(shipping))
	start, matching, status := startPickup(t, h, token, aStart(shipping))
	if status != http.StatusOK || stale == nil || matching == nil {
		t.Fatal("could not prepare independent pickup selections")
	}
	nonce, _ := hiddenInputValue(start, "ExtraData")
	target, _, _ := theMapAnswers(t, h, nonce, "131386", "南港園區", "台北市南港區三重路19-2號")
	page, status := openTheCheckout(t, h, token, target[len("/checkout"):], stale, matching)
	if status != http.StatusOK {
		t.Fatalf("duplicate-cookie return = %d, want 200", status)
	}
	if got, _ := hiddenInputValue(page, "pickup_n"); got != nonce {
		t.Fatalf("rendered placement nonce = %q, want returned nonce %q", got, nonce)
	}
	if !strings.Contains(page, `name="invoice_type" value="mobile_carrier" checked`) {
		t.Fatal("matching cookie did not restore the invoice choice")
	}
	number := placeThisCheckout(t, h, token, matching, page, shipping, "map-duplicate-cookie", stale)
	_, chain, code, name := destinationOf(t, number)
	if chain != "seven_eleven" || code != "131386" || name != "南港園區" {
		t.Fatalf("placed destination = %q/%q/%q", chain, code, name)
	}
}

// inputValue is the value of the first input carrying name, which is what the
// browser would show in that field.
func inputValue(body, name string) (string, bool) {
	tag := regexp.MustCompile(`<input[^>]*\bname="` + regexp.QuoteMeta(name) + `"[^>]*>`).FindString(body)
	if tag == "" {
		return "", false
	}
	m := regexp.MustCompile(`\bvalue="([^"]*)"`).FindStringSubmatch(tag)
	if m == nil {
		return "", false
	}
	return html.UnescapeString(m[1]), true
}

// aTypedStart is 「選擇門市」 pressed on a form the shopper has filled in.
func aTypedStart(shipping uuid.UUID, couponCode string) url.Values {
	fields := aStart(shipping)
	fields.Set("email", "typed-before-the-map@example.com")
	fields.Set("name", "王小明")
	fields.Set("phone", "0912345678")
	fields.Set("note", "請在下午送達")
	fields.Set("invoice_carrier", "/ABC+123")
	fields.Set("coupon", couponCode)
	return fields
}

// TestWhatWasTypedSurvivesTheMapRoundTrip is the owner's bug: choose a store on
// the carrier's map, come back, and the email, phone and everything else typed
// are gone. The form is posted to the start route, the carrier's page posts the
// store back, and the checkout it returns to carries the store AND every field.
func TestWhatWasTypedSurvivesTheMapRoundTrip(t *testing.T) {
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler), false, testLimiter(), nil, configuredMap(t))
	token, shipping := aPickupCart(t, s, "map-typed")
	code := coupon(t, "MAPTYPED", "amount", 5000, 0, 0, 0, 0)

	start, cookie, status := startPickup(t, h, token, aTypedStart(shipping, code))
	if status != http.StatusOK {
		t.Fatalf("the start = %d, want 200", status)
	}
	// What was typed goes to goen's draft and never to the carrier's form.
	for _, typed := range []string{"typed-before-the-map@example.com", "王小明", "0912345678", "請在下午送達"} {
		if strings.Contains(start, typed) {
			t.Errorf("the hand-off page carries %q, which belongs to the shopper and not the carrier", typed)
		}
	}
	nonce, _ := hiddenInputValue(start, "ExtraData")

	target, _, _ := theMapAnswers(t, h, nonce, "131386", "南港園區", "台北市南港區三重路19-2號")
	page, status := openTheCheckout(t, h, token, target[len("/checkout"):], cookie)
	if status != http.StatusOK {
		t.Fatalf("the checkout after the return = %d, want 200", status)
	}
	for field, want := range map[string]string{
		"email": "typed-before-the-map@example.com", "name": "王小明", "phone": "0912345678",
		"invoice_carrier": "/ABC+123", "coupon": code,
	} {
		if got, ok := inputValue(page, field); !ok || got != want {
			t.Errorf("after the map the %s field is %q (present %v), want %q", field, got, ok, want)
		}
	}
	if !strings.Contains(page, "請在下午送達") {
		t.Error("the note the shopper typed did not come back")
	}
	if !strings.Contains(page, "南港園區") || !strings.Contains(page, "131386") {
		t.Error("the store chosen on the map is not on the returned checkout")
	}
	if !strings.Contains(page, `name="invoice_type" value="mobile_carrier" checked`) {
		t.Error("the 發票 choice did not come back")
	}
	// Applied, not just restored as text: the order summary names the coupon by
	// its description, which only a resolved coupon has.
	if !strings.Contains(page, html.EscapeString("測試折扣")) {
		t.Error("the coupon was restored as text but not applied to the quote")
	}

	// A plain visit restores nothing: the draft is for the way back from the map.
	plain, _ := openTheCheckout(t, h, token, "")
	if got, _ := inputValue(plain, "phone"); got != "" {
		t.Errorf("a plain visit to the checkout restored the phone %q", got)
	}
}

// TestAPlacedOrderClearsTheDraft: what was typed is kept for the trip to the map
// and for nothing else, so the order it was typed for ends it.
func TestAPlacedOrderClearsTheDraft(t *testing.T) {
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler), false, testLimiter(), nil, configuredMap(t))
	token, shipping := aPickupCart(t, s, "map-draft-cleared")
	cartID, err := s.ByToken(t.Context(), token, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("find the cart: %v", err)
	}
	draftHeld := func() bool {
		var held bool
		if err := pool.QueryRow(t.Context(),
			`SELECT checkout_draft IS NOT NULL FROM carts WHERE id = $1`, cartID).Scan(&held); err != nil {
			t.Fatalf("read the draft: %v", err)
		}
		return held
	}

	start, cookie, _ := startPickup(t, h, token, aTypedStart(shipping, ""))
	if !draftHeld() {
		t.Fatal("the start kept no draft, so this test proves nothing")
	}
	nonce, _ := hiddenInputValue(start, "ExtraData")
	target, _, _ := theMapAnswers(t, h, nonce, "131386", "南港園區", "台北市南港區三重路19-2號")
	page, _ := openTheCheckout(t, h, token, target[len("/checkout"):], cookie)

	placeThisCheckout(t, h, token, cookie, page, shipping, "map-draft-cleared")
	if draftHeld() {
		t.Error("the order was placed and the draft, which holds the shopper's address, is still on the cart")
	}
}

// TestADraftExpires: a draft older than its window is as good as none.
func TestADraftExpires(t *testing.T) {
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler), false, testLimiter(), nil, configuredMap(t))
	token, shipping := aPickupCart(t, s, "map-draft-expired")
	cartID, err := s.ByToken(t.Context(), token, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("find the cart: %v", err)
	}

	start, cookie, _ := startPickup(t, h, token, aTypedStart(shipping, ""))
	nonce, _ := hiddenInputValue(start, "ExtraData")
	if _, err := pool.Exec(t.Context(),
		`UPDATE carts SET checkout_draft_at = now() - interval '2 hours' WHERE id = $1`, cartID); err != nil {
		t.Fatalf("age the draft: %v", err)
	}
	target, _, _ := theMapAnswers(t, h, nonce, "131386", "南港園區", "台北市南港區三重路19-2號")
	page, _ := openTheCheckout(t, h, token, target[len("/checkout"):], cookie)
	if got, _ := inputValue(page, "phone"); got != "" {
		t.Errorf("an expired draft restored the phone %q", got)
	}
	if !strings.Contains(page, "南港園區") {
		t.Error("the store should still come back; only the typed fields expire")
	}
}
