package cart

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/ui/pages"
)

// ECPay's convenience-store map. Staging is the default, and staging answers
// with one fixed store instead of showing a map.
const (
	MapStagingBaseURL    = "https://logistics-stage.ecpay.com.tw"
	MapProductionBaseURL = "https://logistics.ecpay.com.tw"
)

// mapPath is the hosted picker, under either base URL.
const mapPath = "/Express/map"

// PickupReturnPath is where the shopper's own browser posts the chosen store.
// It is a cross-site POST carrying none of goen's cookies, so the route is
// exempt from the cross-origin defence and validates shape and nothing else.
const PickupReturnPath = "/checkout/pickup/return"

// LogisticsMode is which contract the merchant holds with ECPay. It decides the
// LogisticsSubType spelling and cannot be guessed from the request: ECPay
// refuses a subtype the merchant did not apply for.
type LogisticsMode string

const (
	// ModeB2C is 大宗寄倉, the merchant-to-consumer contract.
	ModeB2C LogisticsMode = "b2c"
	// ModeC2C is 店到店, the store-to-store contract.
	ModeC2C LogisticsMode = "c2c"
)

// mapSubtypes is every chain the checkout offers, in both contracts. ECPay's
// own set is larger; these two are what CheckoutPickupBrandChoices offers, and
// a brand outside it has no subtype rather than a guessed one.
var mapSubtypes = map[LogisticsMode]map[pickup.Brand]string{
	ModeB2C: {
		pickup.SevenEleven: "UNIMART",
		pickup.FamilyMart:  "FAMI",
	},
	ModeC2C: {
		pickup.SevenEleven: "UNIMARTC2C",
		pickup.FamilyMart:  "FAMIC2C",
	},
}

// Map is ECPay's hosted store picker. The zero value is DISABLED: the checkout
// then asks for a chain alone, exactly as it did before this existed.
type Map struct {
	merchantID string
	mode       LogisticsMode
	// action is the full URL the map form posts to, and origin is the scheme
	// and host of it that the Content-Security-Policy names.
	action string
	origin string
	// returnURL is built from the configured public base URL. Never from the
	// request's Host header: that is a value the client chooses.
	returnURL string
}

// NewMap returns the store picker for these credentials, or a disabled one when
// no logistics mode is configured. It follows the 加值中心 posture beside it:
// half a configuration refuses to start rather than failing at the first use.
func NewMap(merchantID, mode, baseURL, siteBaseURL string) (*Map, error) {
	if mode == "" {
		return &Map{}, nil
	}
	m := LogisticsMode(mode)
	if _, ok := mapSubtypes[m]; !ok {
		// i18n-exempt: a startup failure, read by whoever is deploying this.
		return nil, fmt.Errorf("cart: GOEN_ECPAY_LOGISTICS is %q; it must be %q or %q, "+
			"whichever logistics contract the merchant holds", mode, ModeC2C, ModeB2C)
	}
	if merchantID == "" {
		return nil, errors.New("cart: GOEN_ECPAY_LOGISTICS is set without " +
			"GOEN_ECPAY_MERCHANT_ID — the map request is identified by the merchant id " +
			"and ECPay refuses a call without one")
	}
	if baseURL == "" {
		baseURL = MapStagingBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("cart: GOEN_ECPAY_LOGISTICS_BASE_URL %q is not usable: %w", baseURL, err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("cart: GOEN_ECPAY_LOGISTICS_BASE_URL %q must be an absolute "+
			"HTTP(S) URL without userinfo", baseURL)
	}
	if siteBaseURL == "" {
		return nil, errors.New("cart: the store map needs GOEN_BASE_URL: ECPay sends the " +
			"chosen store to that origin, and a URL guessed from the request's Host header " +
			"is a URL the client chose")
	}
	return &Map{
		merchantID: merchantID,
		mode:       m,
		action:     strings.TrimSuffix(baseURL, "/") + mapPath,
		origin:     parsed.Scheme + "://" + parsed.Host,
		returnURL:  strings.TrimSuffix(siteBaseURL, "/") + PickupReturnPath,
	}, nil
}

// Enabled reports whether this deployment offers the store picker.
func (m *Map) Enabled() bool { return m != nil && m.merchantID != "" }

// Origin is the one third-party origin form-action has to name, or "" when the
// picker is off and the policy stays exactly what it was.
func (m *Map) Origin() string {
	if !m.Enabled() {
		return ""
	}
	return m.origin
}

// Subtype is ECPay's LogisticsSubType for this chain under the configured
// contract, and false for a chain the checkout does not offer.
func (m *Map) Subtype(b pickup.Brand) (string, bool) {
	if !m.Enabled() {
		return "", false
	}
	s, ok := mapSubtypes[m.mode][b]
	return s, ok
}

// BrandFor is the reverse: the chain a returned LogisticsSubType names, under
// the configured contract only. A subtype from the other contract is refused,
// because it cannot have come from a map call this deployment made.
func (m *Map) BrandFor(subtype string) (pickup.Brand, bool) {
	if !m.Enabled() {
		return "", false
	}
	for brand, s := range mapSubtypes[m.mode] {
		if s == subtype {
			return brand, true
		}
	}
	return "", false
}

// Request builds the map form for one chain, or false when the picker is off or
// the chain has no subtype under this contract. The form carries ECPay's own
// request parameters and NOTHING else: the checkout's name, phone, e-mail and
// street address must never ride along to a third party.
//
// tradeNo is fresh per render, and the nonce travels in ExtraData, which ECPay
// echoes back unchanged.
func (m *Map) Request(b pickup.Brand, tradeNo, nonce string, mobile bool) (pages.CheckoutMapForm, bool) {
	subtype, ok := m.Subtype(b)
	if !ok {
		return pages.CheckoutMapForm{}, false
	}
	f := pages.CheckoutMapForm{
		Action:           m.action,
		MerchantID:       m.merchantID,
		MerchantTradeNo:  tradeNo,
		LogisticsType:    "CVS",
		LogisticsSubType: subtype,
		// N: the order is paid at goen or at Stripe. A map call saying Y would
		// offer 貨到付款 at the counter for money nobody is expecting.
		IsCollection:   "N",
		ServerReplyURL: m.returnURL,
		ExtraData:      nonce,
	}
	if mobile {
		// 7-ELEVEN serves a different map to a phone and honours whatever value
		// it is given; 全家's map is responsive and ignores this entirely.
		f.Device = "1"
	}
	return f, true
}

// MerchantID is the configured id, which the map form prints and the return
// handler requires the callback to repeat. It is public information.
func (m *Map) MerchantID() string { return m.merchantID }

// NewMerchantTradeNo is a fresh per-render correlation value. ECPay wants it
// unique per call and never reuses it for anything; it is deliberately NOT the
// nonce, which would put a browser-bound secret in a field the map echoes into
// its own systems and logs.
func NewMerchantTradeNo() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const length = 20
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b), nil
}

// nonceBytes gives 80 bits, which fits ExtraData's 20 characters as hex.
const nonceBytes = 10

// newPickupNonce returns the browser-bound secret that decides whether a store
// coming back in the URL is honoured.
func newPickupNonce() (string, error) {
	b := make([]byte, nonceBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// validNonce reports whether s has the exact shape newPickupNonce produces.
func validNonce(s string) bool {
	if len(s) != nonceBytes*2 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// PickupCookieName binds the nonce to this exact origin. A cookie claiming
// __Host- without Secure is rejected by the browser, so the development path
// uses a different name, as every other cookie here does.
const PickupCookieName = "__Host-goen_pickup"

func pickupCookieName(secure bool) string {
	if secure {
		return PickupCookieName
	}
	return "goen_pickup"
}

// pickupCookieMaxAge is how long a shopper has to choose a store and come back.
// The map itself times out after three idle minutes; this is the outer bound on
// how long a nonce left in the address bar is worth anything.
const pickupCookieMaxAge = 2 * 60 * 60

// pickupState is what the pickup cookie carries. The three query keys ride with
// the nonce because the return is a cross-site POST: the browser sends this Lax
// cookie on the refreshed GET, and it is the only thing that survives the trip.
type pickupState struct {
	Nonce string
	// Ship, Invoice and Address are the exact three query keys Checkout reads.
	// They are re-validated by the code that already validates them, so a
	// tampered cookie chooses nothing a hand-typed URL could not.
	Ship    string
	Invoice string
	Address string
}

func writePickupCookie(w http.ResponseWriter, s pickupState, secure bool) {
	raw := strings.Join([]string{s.Nonce, s.Ship, s.Invoice, s.Address}, "|")
	//nolint:gosec // G124: Secure follows the deployment's own flag, as every cookie here does
	http.SetCookie(w, &http.Cookie{
		Name:     pickupCookieName(secure),
		Value:    base64.RawURLEncoding.EncodeToString([]byte(raw)),
		Path:     "/",
		MaxAge:   pickupCookieMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func readPickupCookie(r *http.Request, secure bool) (pickupState, bool) {
	// POST state belongs to the submitted form, never to an unrelated query.
	nonce := r.URL.Query().Get("pickup_n")
	if r.Method == http.MethodPost {
		nonce = r.PostFormValue("pickup_n")
	}
	for _, c := range r.CookiesNamed(pickupCookieName(secure)) {
		raw, err := base64.RawURLEncoding.DecodeString(c.Value)
		if err != nil {
			continue
		}
		parts := strings.SplitN(string(raw), "|", 4)
		if len(parts) != 4 || !validNonce(parts[0]) {
			continue
		}
		// Browsers can send same-name cookies from different paths. Keep the
		// matching state through the callback, re-render and placement alike.
		if nonce != "" && subtle.ConstantTimeCompare([]byte(nonce), []byte(parts[0])) != 1 {
			continue
		}
		return pickupState{
			Nonce: parts[0], Ship: parts[1], Invoice: parts[2], Address: parts[3],
		}, true
	}
	return pickupState{}, false
}

// clearPickupCookie removes it. A nonce left behind after an order is placed
// would still honour a store for the next checkout in this browser.
func clearPickupCookie(w http.ResponseWriter, secure bool) {
	//nolint:gosec // G124: as above
	http.SetCookie(w, &http.Cookie{
		Name: pickupCookieName(secure), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

// PostedStore is a store as it arrives from the URL or from the checkout form.
// Nothing about it is evidence: the map callback carries no signature, and the
// map itself is run by the chain rather than by ECPay.
type PostedStore struct {
	Brand pickup.Brand
	Code  string
	Name  string
	// Nonce is what decides the whole question. It is the only field with
	// weight; the rest are a delivery address a shopper could already type.
	Nonce string
}

// Empty reports whether nothing about a store was submitted at all, which is
// the ordinary checkout and not a refusal.
func (p PostedStore) Empty() bool {
	return p.Brand == "" && p.Code == "" && p.Name == "" && p.Nonce == ""
}

// honourPickupStore is the ONE rule, in ONE place: a store is honoured only
// when the nonce that came back equals the nonce in this browser's own Lax
// cookie, compared in constant time, and the chain is one the checkout offers.
// The chain equality is an internal-consistency check and carries no weight of
// its own — both halves come from the same attacker-influenceable URL.
//
// It runs on the GET that renders a returned store AND on the POST that places
// the order, because otherwise a 422 re-render would carry a dropped store back
// in its own hidden fields.
func honourPickupStore(p PostedStore, state pickupState, known bool) bool {
	if !known || !validNonce(state.Nonce) || !validNonce(p.Nonce) {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(p.Nonce), []byte(state.Nonce)) != 1 {
		return false
	}
	return offeredAtCheckout(p.Brand)
}

// offeredAtCheckout reports whether a shopper may choose this chain today. The
// back office offers more, because an order already placed at one of the others
// has to stay correctable.
func offeredAtCheckout(b pickup.Brand) bool {
	return b == pickup.SevenEleven || b == pickup.FamilyMart
}

// The caps ECPay publishes for the fields it posts back. A value over them did
// not come from the map, whatever it claims.
const (
	maxCallbackStoreNameRunes = 20
	maxCallbackAddressRunes   = 80
	maxCallbackFieldBytes     = 8 << 10
)

// callback is one store as ECPay's own page posts it back.
type callback struct {
	Brand   pickup.Brand
	Code    string
	Name    string
	Address string
	Nonce   string
}

// readCallback validates the SHAPE of a map callback and nothing else. It reads
// no cookie, touches no database, and answers false for anything it does not
// recognise rather than repeating what it was sent.
func (m *Map) readCallback(r *http.Request) (callback, bool) {
	if !m.Enabled() {
		return callback{}, false
	}
	if r.PostFormValue("MerchantID") != m.merchantID {
		return callback{}, false
	}
	brand, ok := m.BrandFor(r.PostFormValue("LogisticsSubType"))
	if !ok {
		return callback{}, false
	}
	// Uppercased first, exactly as Address.Trim does before placement checks the
	// same shape: otherwise the return could refuse a code the order accepts.
	code := strings.ToUpper(strings.TrimSpace(r.PostFormValue("CVSStoreID")))
	if !isStoreCode(code) {
		return callback{}, false
	}
	name := strings.TrimSpace(r.PostFormValue("CVSStoreName"))
	if name == "" || utf8.RuneCountInString(name) > maxCallbackStoreNameRunes || hasControl(name) {
		return callback{}, false
	}
	address := strings.TrimSpace(r.PostFormValue("CVSAddress"))
	if utf8.RuneCountInString(address) > maxCallbackAddressRunes || hasControl(address) {
		return callback{}, false
	}
	nonce := r.PostFormValue("ExtraData")
	if !validNonce(nonce) {
		return callback{}, false
	}
	return callback{Brand: brand, Code: code, Name: name, Address: address, Nonce: nonce}, true
}

// refreshTarget is where the interstitial sends the browser. The path is a
// fixed local literal and every posted value is one encoded query parameter, so
// nothing the callback carries can influence the scheme, the host or the path.
func (c callback) refreshTarget() string {
	q := url.Values{
		"pickup_brand":      {string(c.Brand)},
		"pickup_store_code": {c.Code},
		"pickup_store_name": {c.Name},
		"pickup_store_addr": {c.Address},
		"pickup_n":          {c.Nonce},
	}
	return "/checkout?" + q.Encode()
}
