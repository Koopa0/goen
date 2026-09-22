package cart

import (
	"bytes"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/ui/pages"
)

// aMerchantID stands for whatever id the environment names. It is opaque to
// goen: the only thing done with it is equality against what the callback
// repeats, so a made-up one exercises the rule exactly as the real one would.
const aMerchantID = "1000001"

// aNonce is a nonce of the exact shape newPickupNonce produces.
const aNonce = "0123456789abcdef0123"

// urlWithUserinfo is a base URL the constructor has to refuse. A URL goen posts
// a form to cannot carry credentials, because the browser would send them.
//
//nolint:gosec // G101: the shape under test, not a credential of anybody's
const urlWithUserinfo = "https://user:pass@logistics.example"

func testMap(t *testing.T, mode LogisticsMode) *Map {
	t.Helper()
	m, err := NewMap(aMerchantID, string(mode), "", "https://goen.test")
	if err != nil {
		t.Fatalf("build the store map: %v", err)
	}
	return m
}

func TestTheStoreMapIsOffUntilAContractIsNamed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		merchantID string
		mode       string
		base       string
		site       string
		wantErr    bool
		wantOn     bool
		wantAction string
		wantReply  string
		wantCSP    string
	}{
		{
			name: "no mode is the feature off, and off is not an error",
			mode: "", merchantID: aMerchantID, site: "https://goen.test",
		},
		{
			name: "off even with no merchant id at all",
			mode: "", site: "https://goen.test",
		},
		{
			name:       "b2c defaults to the staging map",
			merchantID: aMerchantID, mode: "b2c", site: "https://goen.test",
			wantOn:     true,
			wantAction: "https://logistics-stage.ecpay.com.tw/Express/map",
			wantReply:  "https://goen.test/checkout/pickup/return",
			wantCSP:    "https://logistics-stage.ecpay.com.tw",
		},
		{
			name:       "production is a base URL and nothing else",
			merchantID: aMerchantID, mode: "c2c",
			base: MapProductionBaseURL, site: "https://goen.test",
			wantOn:     true,
			wantAction: "https://logistics.ecpay.com.tw/Express/map",
			wantReply:  "https://goen.test/checkout/pickup/return",
			wantCSP:    "https://logistics.ecpay.com.tw",
		},
		{
			name: "a mode with no merchant id refuses to start",
			mode: "b2c", site: "https://goen.test", wantErr: true,
		},
		{
			name:       "an unknown mode refuses to start",
			merchantID: aMerchantID, mode: "B2C2C", site: "https://goen.test",
			wantErr: true,
		},
		{
			name:       "no public base URL refuses to start",
			merchantID: aMerchantID, mode: "b2c", wantErr: true,
		},
		{
			name:       "a base URL that is not http(s) refuses to start",
			merchantID: aMerchantID, mode: "b2c",
			base: "ftp://logistics.example", site: "https://goen.test", wantErr: true,
		},
		{
			name:       "a base URL carrying credentials refuses to start",
			merchantID: aMerchantID, mode: "b2c",
			base: urlWithUserinfo, site: "https://goen.test", wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := NewMap(tt.merchantID, tt.mode, tt.base, tt.site)
			if tt.wantErr {
				if err == nil {
					t.Fatal("started on half a configuration, which is the failure that " +
						"is only noticed after a shopper has been to the carrier")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewMap: %v", err)
			}
			if m.Enabled() != tt.wantOn {
				t.Fatalf("Enabled() = %v, want %v", m.Enabled(), tt.wantOn)
			}
			if !tt.wantOn {
				if got := m.Origin(); got != "" {
					t.Errorf("a disabled map names %q for the policy; the policy must "+
						"be byte-identical to main's", got)
				}
				return
			}
			form, ok := m.Request(pickup.SevenEleven, "TRADE", aNonce, false)
			if !ok {
				t.Fatal("a configured map offers no form for a chain the checkout offers")
			}
			if form.Action != tt.wantAction {
				t.Errorf("action = %q, want %q", form.Action, tt.wantAction)
			}
			if form.ServerReplyURL != tt.wantReply {
				t.Errorf("ServerReplyURL = %q, want %q — it is built from the "+
					"configured origin and never from a request's Host header",
					form.ServerReplyURL, tt.wantReply)
			}
			if got := m.Origin(); got != tt.wantCSP {
				t.Errorf("Origin() = %q, want %q", got, tt.wantCSP)
			}
		})
	}
}

// TestTheMapFormCarriesTheContractAndNothingElse holds the subtype table, which
// is the one field that differs between the two contracts, and the fixed values
// ECPay's map requires.
func TestTheMapFormCarriesTheContractAndNothingElse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mode    LogisticsMode
		brand   pickup.Brand
		want    string
		offered bool
	}{
		{mode: ModeB2C, brand: pickup.SevenEleven, want: "UNIMART", offered: true},
		{mode: ModeB2C, brand: pickup.FamilyMart, want: "FAMI", offered: true},
		{mode: ModeC2C, brand: pickup.SevenEleven, want: "UNIMARTC2C", offered: true},
		{mode: ModeC2C, brand: pickup.FamilyMart, want: "FAMIC2C", offered: true},
		// ECPay's set has no OK mart at all, in either contract, so the checkout
		// cannot open a map for one and must not pretend it can.
		{mode: ModeB2C, brand: pickup.OKMart},
		{mode: ModeC2C, brand: pickup.OKMart},
		{mode: ModeB2C, brand: pickup.HiLife},
		{mode: ModeB2C, brand: ""},
	}

	for _, tt := range tests {
		t.Run(string(tt.mode)+"/"+string(tt.brand), func(t *testing.T) {
			t.Parallel()
			m := testMap(t, tt.mode)
			form, ok := m.Request(tt.brand, "TRADE", aNonce, false)
			if ok != tt.offered {
				t.Fatalf("Request offered = %v, want %v", ok, tt.offered)
			}
			if !tt.offered {
				return
			}
			if form.LogisticsSubType != tt.want {
				t.Errorf("LogisticsSubType = %q, want %q", form.LogisticsSubType, tt.want)
			}
			if form.LogisticsType != "CVS" {
				t.Errorf("LogisticsType = %q, want CVS", form.LogisticsType)
			}
			if form.IsCollection != "N" {
				t.Errorf("IsCollection = %q, want N: Y asks the counter to collect "+
					"money nobody is expecting", form.IsCollection)
			}
			if form.ExtraData != aNonce {
				t.Errorf("ExtraData = %q, want the nonce %q", form.ExtraData, aNonce)
			}
			if form.MerchantID != aMerchantID {
				t.Errorf("MerchantID = %q, want %q", form.MerchantID, aMerchantID)
			}
			if form.Device != "" {
				t.Errorf("Device = %q on a desktop render, want it absent", form.Device)
			}
		})
	}

	mobile, _ := testMap(t, ModeB2C).Request(pickup.SevenEleven, "TRADE", aNonce, true)
	if mobile.Device != "1" {
		t.Errorf("Device = %q on a phone, want 1", mobile.Device)
	}
}

// TestACorrelationNumberIsNeverTheNonce pins the one finding the security
// review made about the map request itself: MerchantTradeNo reaches ECPay's own
// systems and logs, and the nonce is a browser-bound secret.
func TestACorrelationNumberIsNeverTheNonce(t *testing.T) {
	t.Parallel()

	first, err := NewMerchantTradeNo()
	if err != nil {
		t.Fatalf("new correlation number: %v", err)
	}
	second, err := NewMerchantTradeNo()
	if err != nil {
		t.Fatalf("new correlation number: %v", err)
	}
	if first == second {
		t.Error("two renders produced the same MerchantTradeNo; ECPay requires it unique per call")
	}
	if len(first) != 20 {
		t.Errorf("MerchantTradeNo is %d characters, want 20 — ECPay's own cap", len(first))
	}
	if validNonce(first) {
		t.Error("a correlation number has the shape of a nonce; the two must not be confusable")
	}

	nonce, err := newPickupNonce()
	if err != nil {
		t.Fatalf("new nonce: %v", err)
	}
	if !validNonce(nonce) {
		t.Errorf("newPickupNonce produced %q, which validNonce refuses", nonce)
	}
	other, err := newPickupNonce()
	if err != nil {
		t.Fatalf("new nonce: %v", err)
	}
	if other == nonce {
		t.Error("two nonces were equal")
	}
}

func TestANonceShapeIsExactlyTwentyLowercaseHex(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		in   string
		want bool
	}{
		{in: aNonce, want: true},
		{in: "", want: false},
		{in: "0123456789abcdef012", want: false},
		{in: "0123456789abcdef01234", want: false},
		{in: "0123456789ABCDEF0123", want: false},
		{in: "0123456789abcdef012g", want: false},
		{in: "0123456789abcdef012 ", want: false},
	} {
		if got := validNonce(tt.in); got != tt.want {
			t.Errorf("validNonce(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// TestOnlyThisBrowsersOwnStoreIsHonoured is the ONE security rule, in the one
// place it lives. Both attacks the design names end here.
func TestOnlyThisBrowsersOwnStoreIsHonoured(t *testing.T) {
	t.Parallel()

	const otherNonce = "fedcba98765432100000"
	held := pickupState{Nonce: aNonce}

	tests := []struct {
		name   string
		posted PostedStore
		state  pickupState
		known  bool
		want   bool
	}{
		{
			name:   "the store this browser went to fetch",
			posted: PostedStore{Brand: pickup.SevenEleven, Code: "131386", Name: "南港園區", Nonce: aNonce},
			state:  held, known: true, want: true,
		},
		{
			name:   "the other offered chain, also fine",
			posted: PostedStore{Brand: pickup.FamilyMart, Code: "012345", Name: "松高店", Nonce: aNonce},
			state:  held, known: true, want: true,
		},
		{
			// T1: an attacker page auto-posts a forged store in the victim's
			// browser. It cannot know the nonce, so the store is dropped.
			name:   "a forged store carrying a nonce of the attacker's own",
			posted: PostedStore{Brand: pickup.SevenEleven, Code: "999999", Name: "假門市", Nonce: otherNonce},
			state:  held, known: true,
		},
		{
			// T2: a crafted /checkout?… link, which carries no nonce at all.
			name:   "a crafted link with no nonce",
			posted: PostedStore{Brand: pickup.SevenEleven, Code: "999999", Name: "假門市"},
			state:  held, known: true,
		},
		{
			name:   "the right nonce but no cookie in this browser",
			posted: PostedStore{Brand: pickup.SevenEleven, Code: "131386", Name: "南港園區", Nonce: aNonce},
			state:  pickupState{},
		},
		{
			name:   "a chain the checkout does not offer",
			posted: PostedStore{Brand: pickup.OKMart, Code: "131386", Name: "南港園區", Nonce: aNonce},
			state:  held, known: true,
		},
		{
			name:   "no chain at all",
			posted: PostedStore{Code: "131386", Name: "南港園區", Nonce: aNonce},
			state:  held, known: true,
		},
		{
			name:   "an empty nonce on both sides is not a match",
			posted: PostedStore{Brand: pickup.SevenEleven, Code: "131386", Name: "南港園區"},
			state:  pickupState{}, known: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := honourPickupStore(tt.posted, tt.state, tt.known); got != tt.want {
				t.Errorf("honourPickupStore = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestTheCallbackIsCheckedForShapeAndNothingElse is R3: every rejection the
// return handler makes, over the fields ECPay documents.
func TestTheCallbackIsCheckedForShapeAndNothingElse(t *testing.T) {
	t.Parallel()

	good := func() url.Values {
		return url.Values{
			"MerchantID":       {aMerchantID},
			"MerchantTradeNo":  {"ABCDEFGHIJ1234567890"},
			"LogisticsSubType": {"UNIMART"},
			"CVSStoreID":       {"131386"},
			"CVSStoreName":     {"南港園區"},
			"CVSAddress":       {"台北市南港區三重路19-2號"},
			"CVSOutSide":       {"0"},
			"ExtraData":        {aNonce},
		}
	}

	tests := []struct {
		name  string
		edit  func(url.Values)
		want  bool
		brand pickup.Brand
	}{
		{name: "as ECPay posts it", want: true, brand: pickup.SevenEleven},
		{
			name: "7-ELEVEN omits CVSTelephone, which goen never asked for",
			edit: func(v url.Values) { v.Del("CVSTelephone") },
			want: true, brand: pickup.SevenEleven,
		},
		{
			name: "the other chain under the same contract",
			edit: func(v url.Values) { v.Set("LogisticsSubType", "FAMI") },
			want: true, brand: pickup.FamilyMart,
		},
		{
			name: "another merchant's callback",
			edit: func(v url.Values) { v.Set("MerchantID", "1000002") },
		},
		{
			name: "no merchant at all",
			edit: func(v url.Values) { v.Del("MerchantID") },
		},
		{
			name: "the OTHER contract's spelling of the same chain",
			edit: func(v url.Values) { v.Set("LogisticsSubType", "UNIMARTC2C") },
		},
		{
			name: "a chain ECPay serves and this checkout does not offer",
			edit: func(v url.Values) { v.Set("LogisticsSubType", "HILIFE") },
		},
		{
			name: "no subtype",
			edit: func(v url.Values) { v.Del("LogisticsSubType") },
		},
		{
			name: "a store code longer than any store number",
			edit: func(v url.Values) { v.Set("CVSStoreID", "12345678901") },
		},
		{
			name: "a store code with punctuation in it",
			edit: func(v url.Values) { v.Set("CVSStoreID", "1234-56") },
		},
		{
			name: "no store code",
			edit: func(v url.Values) { v.Del("CVSStoreID") },
		},
		{
			name: "no store name",
			edit: func(v url.Values) { v.Set("CVSStoreName", "   ") },
		},
		{
			name: "a store name past ECPay's own cap",
			edit: func(v url.Values) { v.Set("CVSStoreName", strings.Repeat("門", 21)) },
		},
		{
			name: "a store name carrying a newline",
			edit: func(v url.Values) { v.Set("CVSStoreName", "南港\n園區") },
		},
		{
			name: "an address past ECPay's own cap",
			edit: func(v url.Values) { v.Set("CVSAddress", strings.Repeat("路", 81)) },
		},
		{
			name: "an address carrying a C1 control character",
			edit: func(v url.Values) { v.Set("CVSAddress", "台北市\u0085南港區") },
		},
		{
			name: "no address, which is not a refusal",
			edit: func(v url.Values) { v.Del("CVSAddress") },
			want: true, brand: pickup.SevenEleven,
		},
		{
			name: "ExtraData that is not a nonce",
			edit: func(v url.Values) { v.Set("ExtraData", "hello") },
		},
		{
			name: "no ExtraData",
			edit: func(v url.Values) { v.Del("ExtraData") },
		},
	}

	m := testMap(t, ModeB2C)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			form := good()
			if tt.edit != nil {
				tt.edit(form)
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
				PickupReturnPath, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			store, ok := m.readCallback(req)
			if ok != tt.want {
				t.Fatalf("readCallback ok = %v, want %v", ok, tt.want)
			}
			if !tt.want {
				return
			}
			if store.Brand != tt.brand {
				t.Errorf("brand = %q, want %q", store.Brand, tt.brand)
			}
			if store.Nonce != aNonce {
				t.Errorf("nonce = %q, want the one goen sent", store.Nonce)
			}
		})
	}
}

// TestADisabledMapReadsNoCallbackAtAll is the other half of R4: without a
// carrier there is no route, and the reader behind it would refuse anyway.
func TestADisabledMapReadsNoCallbackAtAll(t *testing.T) {
	t.Parallel()

	off, err := NewMap(aMerchantID, "", "", "https://goen.test")
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, PickupReturnPath,
		strings.NewReader(url.Values{
			"MerchantID": {aMerchantID}, "LogisticsSubType": {"UNIMART"},
			"CVSStoreID": {"131386"}, "CVSStoreName": {"南港園區"}, "ExtraData": {aNonce},
		}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if _, ok := off.readCallback(req); ok {
		t.Error("a disabled map read a callback")
	}
}

// TestALowerCaseStoreCodeIsReadTheWayPlacementReadsIt closes the gap the review
// found: Address.Trim uppercases the code before the same shape rule runs at
// placement, so the return must fold it the same way or it refuses a store the
// order would have accepted.
func TestALowerCaseStoreCodeIsReadTheWayPlacementReadsIt(t *testing.T) {
	t.Parallel()

	form := url.Values{
		"MerchantID": {aMerchantID}, "LogisticsSubType": {"FAMI"},
		"CVSStoreID": {" a12345 "}, "CVSStoreName": {"松高店"}, "ExtraData": {aNonce},
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		PickupReturnPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	store, ok := testMap(t, ModeB2C).readCallback(req)
	if !ok {
		t.Fatal("a store code in lower case was refused; placement would have accepted it")
	}
	if store.Code != "A12345" {
		t.Errorf("code = %q, want A12345 — the same folding Address.Trim does", store.Code)
	}
}

// TestNothingPostedReachesTheSchemeHostOrPathOfTheRefresh is the rule that, if
// dropped, turns a CSRF-exempt endpoint into an open redirect.
func TestNothingPostedReachesTheSchemeHostOrPathOfTheRefresh(t *testing.T) {
	t.Parallel()

	hostile := []string{
		`" onload="alert(1)`,
		"https://evil.example/",
		"//evil.example/",
		`\\evil.example`,
		"x?next=https://evil.example",
		"x#/../../evil",
		"店\n名",
		"店;名",
		"店<名>",
	}

	for _, s := range hostile {
		got := callback{
			Brand: pickup.SevenEleven, Code: "131386", Name: s, Address: s, Nonce: aNonce,
		}.refreshTarget()

		if !strings.HasPrefix(got, "/checkout?") {
			t.Errorf("refreshTarget(%q) = %q, which does not start at goen's own checkout", s, got)
			continue
		}
		u, err := url.Parse(got)
		if err != nil {
			t.Errorf("refreshTarget(%q) = %q, which will not parse: %v", s, got, err)
			continue
		}
		if u.Scheme != "" || u.Host != "" || u.Opaque != "" {
			t.Errorf("refreshTarget(%q) reached outside goen: scheme=%q host=%q",
				s, u.Scheme, u.Host)
		}
		if u.Path != "/checkout" {
			t.Errorf("refreshTarget(%q) has path %q, want /checkout", s, u.Path)
		}
		if name := u.Query().Get("pickup_store_name"); name != s {
			t.Errorf("the store name came back as %q, want %q — encoding must not "+
				"lose it either", name, s)
		}
		if nonce := u.Query().Get("pickup_n"); nonce != aNonce {
			t.Errorf("the nonce came back as %q", nonce)
		}
	}
}

// TestThePickupCookieFollowsTheHouseNaming holds R2, including the development
// name: a __Host- cookie without Secure is rejected outright by the browser,
// and the whole feature would silently never work on a plain-HTTP dev server.
func TestThePickupCookieFollowsTheHouseNaming(t *testing.T) {
	t.Parallel()

	for _, secure := range []bool{true, false} {
		want := "goen_pickup"
		if secure {
			want = PickupCookieName
		}
		if got := pickupCookieName(secure); got != want {
			t.Fatalf("pickupCookieName(%v) = %q, want %q", secure, got, want)
		}

		res := httptest.NewRecorder()
		state := pickupState{Nonce: aNonce, Ship: "ship-1", Invoice: "mobile", Address: "addr-7"}
		writePickupCookie(res, state, secure)

		c := res.Result().Cookies()[0]
		if c.Name != want {
			t.Errorf("cookie name = %q, want %q", c.Name, want)
		}
		if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
			t.Errorf("cookie is HttpOnly=%v SameSite=%v Path=%q; Lax is what makes the "+
				"refreshed GET after a cross-site POST carry it at all",
				c.HttpOnly, c.SameSite, c.Path)
		}
		if c.Secure != secure {
			t.Errorf("cookie Secure = %v, want %v", c.Secure, secure)
		}

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/checkout", http.NoBody)
		req.AddCookie(c)
		got, ok := readPickupCookie(req, secure)
		if !ok {
			t.Fatal("the cookie goen just wrote did not read back")
		}
		if got != state {
			t.Errorf("read back %+v, want %+v", got, state)
		}
	}
}

func TestAMangledPickupCookieIsNoCookieAtAll(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"", "!!!not base64!!!", encodeState("nonce-with-the-wrong-shape|a|b|c"),
		encodeState(aNonce + "|only|three"), encodeState("|a|b|c"),
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/checkout", http.NoBody)
		//nolint:gosec // G124: a deliberately mangled cookie, read and refused
		req.AddCookie(&http.Cookie{Name: "goen_pickup", Value: value})
		if _, ok := readPickupCookie(req, false); ok {
			t.Errorf("readPickupCookie accepted %q", value)
		}
	}
}

// TestClearingThePickupCookieExpiresIt holds the last line of R2: a nonce left
// behind would vouch for a store in the next checkout this browser starts.
func TestClearingThePickupCookieExpiresIt(t *testing.T) {
	t.Parallel()

	res := httptest.NewRecorder()
	clearPickupCookie(res, false)
	c := res.Result().Cookies()[0]
	if c.Value != "" || c.MaxAge >= 0 {
		t.Errorf("clearing wrote value %q MaxAge %d, want an expired empty cookie",
			c.Value, c.MaxAge)
	}
}

// encodeState is the cookie's own encoding, for the cases that feed it rubbish.
func encodeState(raw string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func TestPickupUsesTheMatchingCookieAcrossCheckoutRequests(t *testing.T) {
	t.Parallel()
	const staleNonce = "fedcba98765432100000"
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, secure := range []bool{false, true} {
			for _, prefix := range []string{encodeState(staleNonce + "|old-ship|old-invoice|old-address"), "bad-cookie"} {
				req := httptest.NewRequestWithContext(t.Context(), method,
					"/checkout?pickup_n="+aNonce, strings.NewReader("pickup_n="+aNonce))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				for _, value := range []string{prefix, encodeState(aNonce + "|chosen-ship|mobile_carrier|chosen-address")} {
					//nolint:gosec // G124: both production and local cookie names are exercised
					req.AddCookie(&http.Cookie{Name: pickupCookieName(secure), Value: value})
				}
				got, ok := readPickupCookie(req, secure)
				want := pickupState{Nonce: aNonce, Ship: "chosen-ship", Invoice: "mobile_carrier", Address: "chosen-address"}
				if !ok || got != want {
					t.Fatalf("%s secure=%v matching cookie = %+v/%v, want %+v", method, secure, got, ok, want)
				}
			}
		}
	}
}

func TestPickupRefusalDiagnosticsDoNotExposeSelectionSecrets(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	h := &Handler{storeMap: testMap(t, ModeB2C), log: slog.New(slog.NewJSONHandler(&logs, nil))}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/checkout?pickup_n="+aNonce+"&pickup_brand=seven_eleven&pickup_store_code=131386&pickup_store_name=private-store", http.NoBody)
	//nolint:gosec // G124: stale local cookie used to exercise refusal diagnostics
	req.AddCookie(&http.Cookie{Name: "goen_pickup", Value: encodeState("fedcba98765432100000|a|b|c")})
	view := &pages.CheckoutView{}
	if got := h.applyReturnedStore(req, view); got != http.StatusUnprocessableEntity || !view.PickupRefused {
		t.Fatalf("unmatched selection = %d, refused=%v", got, view.PickupRefused)
	}
	for _, want := range []string{`"pickup_cookie_count":1`, `"nonce_valid":true`, `"nonce_matched":false`, `"brand_offered":true`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("diagnostics missing %s: %s", want, logs.String())
		}
	}
	for _, secret := range []string{aNonce, "fedcba98765432100000", "131386", "private-store"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("diagnostics disclosed selection data %q", secret)
		}
	}
}

func TestMissingPickupNonceIsNotLoggedAsAMatch(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	h := &Handler{storeMap: testMap(t, ModeB2C), log: slog.New(slog.NewJSONHandler(&logs, nil))}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/checkout?pickup_brand=seven_eleven&pickup_store_code=131386", http.NoBody)
	//nolint:gosec // G124: local selection cookie without a matching return nonce
	req.AddCookie(&http.Cookie{Name: "goen_pickup", Value: encodeState(aNonce + "|a|b|c")})
	view := &pages.CheckoutView{}
	if got := h.applyReturnedStore(req, view); got != http.StatusUnprocessableEntity {
		t.Fatalf("return without a nonce = %d, want 422", got)
	}
	if !strings.Contains(logs.String(), `"nonce_matched":false`) {
		t.Errorf("missing nonce reported as a match: %s", logs.String())
	}
}
