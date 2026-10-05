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
	"github.com/koopa0/goen/internal/web"
)

// ECPay's convenience-store map. Staging is the default and answers with one
// fixed store instead of a map.
const (
	MapStagingBaseURL    = "https://logistics-stage.ecpay.com.tw"
	MapProductionBaseURL = "https://logistics.ecpay.com.tw"
)

const mapPath = "/Express/map"

// PickupReturnPath receives a cross-site POST carrying none of goen's cookies,
// so the route is exempt from the cross-origin defence and validates shape
// only.
const PickupReturnPath = "/checkout/pickup/return"

// LogisticsMode cannot be guessed from the request: ECPay refuses a
// LogisticsSubType the merchant did not apply for.
type LogisticsMode string

const (
	// ModeB2C is ECPay's 大宗寄倉 contract; ModeC2C is 店到店. A merchant holds one.
	ModeB2C LogisticsMode = "b2c"
	ModeC2C LogisticsMode = "c2c"
)

// mapSubtypes is what CheckoutPickupChainChoices offers; a chain outside it has
// no subtype rather than a guessed one.
var mapSubtypes = map[LogisticsMode]map[pickup.Chain]string{
	ModeB2C: {
		pickup.SevenEleven: "UNIMART",
		pickup.FamilyMart:  "FAMI",
	},
	ModeC2C: {
		pickup.SevenEleven: "UNIMARTC2C",
		pickup.FamilyMart:  "FAMIC2C",
	},
}

// StoreMap is DISABLED at its zero value: the checkout then offers no pickup.
type StoreMap struct {
	merchantID string
	mode       LogisticsMode
	// origin is the scheme and host of action that the Content-Security-Policy
	// names.
	action string
	origin string
	// returnURL is built from the configured public base URL, never the
	// request's Host header, which the client chooses.
	returnURL string
}

// NewStoreMap follows the 加值中心 posture beside it: half a configuration refuses
// to start rather than failing at first use.
func NewStoreMap(merchantID, mode, baseURL, siteBaseURL string) (*StoreMap, error) {
	if mode == "" {
		return &StoreMap{}, nil
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
	return &StoreMap{
		merchantID: merchantID,
		mode:       m,
		action:     strings.TrimSuffix(baseURL, "/") + mapPath,
		origin:     parsed.Scheme + "://" + parsed.Host,
		returnURL:  strings.TrimSuffix(siteBaseURL, "/") + PickupReturnPath,
	}, nil
}

func (m *StoreMap) Enabled() bool { return m != nil && m.merchantID != "" }

// Origin is the one third-party origin form-action must name, or "" when the
// picker is off.
func (m *StoreMap) Origin() string {
	if !m.Enabled() {
		return ""
	}
	return m.origin
}

func (m *StoreMap) Subtype(chain pickup.Chain) (string, bool) {
	if !m.Enabled() {
		return "", false
	}
	s, ok := mapSubtypes[m.mode][chain]
	return s, ok
}

// ChainFor refuses a subtype from the other contract: it cannot have come from
// a map call this deployment made.
func (m *StoreMap) ChainFor(subtype string) (pickup.Chain, bool) {
	if !m.Enabled() {
		return "", false
	}
	for chain, s := range mapSubtypes[m.mode] {
		if s == subtype {
			return chain, true
		}
	}
	return "", false
}

// Request builds a form that carries ECPay's own parameters and NOTHING else: the
// checkout's name, phone, e-mail and address must never ride to a third party.
// tradeNo is fresh per render; the nonce travels in ExtraData, which ECPay
// echoes back unchanged.
func (m *StoreMap) Request(chain pickup.Chain, tradeNo, nonce string, mobile bool) (pages.CheckoutMapForm, bool) {
	subtype, ok := m.Subtype(chain)
	if !ok {
		return pages.CheckoutMapForm{}, false
	}
	f := pages.CheckoutMapForm{
		Action:           m.action,
		MerchantID:       m.merchantID,
		MerchantTradeNo:  tradeNo,
		LogisticsType:    "CVS",
		LogisticsSubType: subtype,
		// N: the order is paid at goen or at Stripe; Y would offer 貨到付款 at the
		// counter for money nobody expects.
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

// MerchantID is public; the return handler requires the callback to repeat it.
func (m *StoreMap) MerchantID() string { return m.merchantID }

// NewMerchantTradeNo is deliberately NOT the nonce, which would put a
// browser-bound secret in a field the map echoes into its own systems and logs.
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

func newPickupNonce() (string, error) {
	b := make([]byte, nonceBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

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

// PickupCookieName binds the nonce to this origin; development uses a different
// name because a __Host- cookie without Secure is rejected.
const PickupCookieName = "__Host-goen_pickup"

func pickupCookieName(secure bool) string {
	if secure {
		return PickupCookieName
	}
	return "goen_pickup"
}

// pickupCookieMaxAge bounds how long a nonce left in the address bar is worth
// anything; the map itself times out after three idle minutes.
const pickupCookieMaxAge = 2 * 60 * 60

// pickupState carries the three query keys with the nonce because the return is
// a cross-site POST: the browser sends this Lax cookie on the refreshed GET,
// the only thing that survives the trip.
type pickupState struct {
	Nonce string
	// Ship, Invoice and Address are the three query keys Checkout reads and
	// re-validates, so a tampered cookie chooses nothing a hand-typed URL could
	// not.
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
	var first pickupState
	var found bool
	for _, c := range r.CookiesNamed(pickupCookieName(secure)) {
		raw, err := base64.RawURLEncoding.DecodeString(c.Value)
		if err != nil {
			continue
		}
		parts := strings.SplitN(string(raw), "|", 4)
		if len(parts) != 4 || !validNonce(parts[0]) {
			continue
		}
		state := pickupState{Nonce: parts[0], Ship: parts[1], Invoice: parts[2], Address: parts[3]}
		// Browsers can send same-name cookies from different paths. The one
		// carrying the returned nonce wins; otherwise the first valid cookie is
		// still this browser's state, and honourPickupStore alone decides
		// whether the store is believed.
		if nonce != "" && subtle.ConstantTimeCompare([]byte(nonce), []byte(parts[0])) == 1 {
			return state, true
		}
		if !found {
			first, found = state, true
		}
	}
	return first, found
}

func pickupNonceMatched(r *http.Request, secure bool, posted string) bool {
	if !validNonce(posted) {
		return false
	}
	matched := false
	for _, c := range r.CookiesNamed(pickupCookieName(secure)) {
		raw, err := base64.RawURLEncoding.DecodeString(c.Value)
		if err != nil {
			continue
		}
		nonce, _, _ := strings.Cut(string(raw), "|")
		if subtle.ConstantTimeCompare([]byte(posted), []byte(nonce)) == 1 {
			matched = true
		}
	}
	return matched
}

// A nonce left behind after an order is placed would still honour a store for
// the next checkout in this browser.
func clearPickupCookie(w http.ResponseWriter, secure bool) {
	//nolint:gosec // G124: as above
	http.SetCookie(w, &http.Cookie{
		Name: pickupCookieName(secure), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

// PostedStore is no evidence: the map callback carries no signature, and the
// map is run by the chain rather than by ECPay.
type PostedStore struct {
	Chain pickup.Chain
	Code  string
	Name  string
	// Nonce is the only field with weight; the rest is a delivery address a
	// shopper could already type.
	Nonce string
}

func (p PostedStore) Empty() bool {
	return p.Chain == "" && p.Code == "" && p.Name == "" && p.Nonce == ""
}

// honourPickupStore is the ONE rule: a store is honoured only when the returned
// nonce equals the nonce in this browser's own Lax cookie, compared in constant
// time, and the chain is one the checkout offers. The chain equality carries no
// weight of its own: both halves come from the same attacker-influenceable URL.
// It runs on the GET that renders a returned store AND on the POST that places
// the order, otherwise a 422 re-render would carry a dropped store back in its
// hidden fields.
func honourPickupStore(p PostedStore, state pickupState, known bool) bool {
	if !known || !validNonce(state.Nonce) || !validNonce(p.Nonce) {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(p.Nonce), []byte(state.Nonce)) != 1 {
		return false
	}
	return offeredAtCheckout(p.Chain)
}

// offeredAtCheckout is narrower than the back office, where an order already
// placed at another chain must stay correctable.
func offeredAtCheckout(chain pickup.Chain) bool {
	return chain == pickup.SevenEleven || chain == pickup.FamilyMart
}

// ECPay's published caps for the posted fields; a value over them did not come
// from the map.
const (
	maxCallbackStoreNameRunes = 20
	maxCallbackAddressRunes   = 80
	maxCallbackFieldBytes     = 8 << 10
)

type callback struct {
	Chain   pickup.Chain
	Code    string
	Name    string
	Address string
	Nonce   string
}

// readCallback validates SHAPE only: no cookie, no database, and false for
// anything unrecognised rather than repeating it.
func (m *StoreMap) readCallback(r *http.Request) (callback, bool) {
	if !m.Enabled() {
		return callback{}, false
	}
	if r.PostFormValue("MerchantID") != m.merchantID {
		return callback{}, false
	}
	chain, ok := m.ChainFor(r.PostFormValue("LogisticsSubType"))
	if !ok {
		return callback{}, false
	}
	// Uppercased first, as Delivery.Trim does before placement checks the same
	// shape, so the return cannot refuse a code the order accepts.
	code := strings.ToUpper(strings.TrimSpace(r.PostFormValue("CVSStoreID")))
	if !pickup.ValidStoreCode(code) {
		return callback{}, false
	}
	name := strings.TrimSpace(r.PostFormValue("CVSStoreName"))
	if name == "" || utf8.RuneCountInString(name) > maxCallbackStoreNameRunes || web.HasControlChars(name) {
		return callback{}, false
	}
	address := strings.TrimSpace(r.PostFormValue("CVSAddress"))
	if utf8.RuneCountInString(address) > maxCallbackAddressRunes || web.HasControlChars(address) {
		return callback{}, false
	}
	nonce := r.PostFormValue("ExtraData")
	if !validNonce(nonce) {
		return callback{}, false
	}
	return callback{Chain: chain, Code: code, Name: name, Address: address, Nonce: nonce}, true
}

// The path is a fixed literal and every posted value is one encoded query
// parameter, so nothing in the callback can influence the scheme, host or path.
func (c callback) refreshTarget() string {
	q := url.Values{
		"pickup_chain":      {string(c.Chain)},
		"pickup_store_code": {c.Code},
		"pickup_store_name": {c.Name},
		"pickup_store_addr": {c.Address},
		"pickup_n":          {c.Nonce},
	}
	return "/checkout?" + q.Encode()
}
