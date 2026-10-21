// Package storemap is ECPay's convenience-store map, where a shopper picks the
// store to collect a parcel from; it holds the map client, the nonce cookie,
// the callback checks, PostedStore and the two hop pages; it does not hold the
// pickup method, chains, carriers or store-code shape (shipping).
package storemap

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

	"github.com/koopa0/goen/internal/shipping"

	"github.com/koopa0/goen/internal/formtext"
)

// ECPay's convenience-store map. Staging is the default and answers with one
// fixed store instead of a map.
const (
	StagingBaseURL    = "https://logistics-stage.ecpay.com.tw"
	ProductionBaseURL = "https://logistics.ecpay.com.tw"
)

const mapPath = "/Express/map"

// ReturnPath receives a cross-site POST carrying none of goen's cookies,
// so the route is exempt from the cross-origin defence and validates shape
// only.
const ReturnPath = "/checkout/pickup/return"

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
var mapSubtypes = map[LogisticsMode]map[shipping.Chain]string{
	ModeB2C: {
		shipping.ChainSevenEleven: "UNIMART",
		shipping.ChainFamilyMart:  "FAMI",
	},
	ModeC2C: {
		shipping.ChainSevenEleven: "UNIMARTC2C",
		shipping.ChainFamilyMart:  "FAMIC2C",
	},
}

// Client is DISABLED at its zero value: the checkout then offers no pickup.
type Client struct {
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

// New follows the 加值中心 posture beside it: half a configuration refuses
// to start rather than failing at first use.
func New(merchantID, mode, baseURL, siteBaseURL string) (*Client, error) {
	if mode == "" {
		return &Client{}, nil
	}
	m := LogisticsMode(mode)
	if _, ok := mapSubtypes[m]; !ok {
		// i18n-exempt: a startup failure, read by whoever is deploying this.
		return nil, fmt.Errorf("storemap: GOEN_ECPAY_LOGISTICS is %q; it must be %q or %q, "+
			"whichever logistics contract the merchant holds", mode, ModeC2C, ModeB2C)
	}
	if merchantID == "" {
		return nil, errors.New("storemap: GOEN_ECPAY_LOGISTICS is set without " +
			"GOEN_ECPAY_MERCHANT_ID — the map request is identified by the merchant id " +
			"and ECPay refuses a call without one")
	}
	if baseURL == "" {
		baseURL = StagingBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("storemap: GOEN_ECPAY_LOGISTICS_BASE_URL %q is not usable: %w", baseURL, err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("storemap: GOEN_ECPAY_LOGISTICS_BASE_URL %q must be an absolute "+
			"HTTP(S) URL without userinfo", baseURL)
	}
	if siteBaseURL == "" {
		return nil, errors.New("storemap: the store map needs GOEN_BASE_URL: ECPay sends the " +
			"chosen store to that origin, and a URL guessed from the request's Host header " +
			"is a URL the client chose")
	}
	return &Client{
		merchantID: merchantID,
		mode:       m,
		action:     strings.TrimSuffix(baseURL, "/") + mapPath,
		origin:     parsed.Scheme + "://" + parsed.Host,
		returnURL:  strings.TrimSuffix(siteBaseURL, "/") + ReturnPath,
	}, nil
}

func (m *Client) Enabled() bool { return m != nil && m.merchantID != "" }

// Origin is the one third-party origin form-action must name, or "" when the
// picker is off.
func (m *Client) Origin() string {
	if !m.Enabled() {
		return ""
	}
	return m.origin
}

func (m *Client) Subtype(chain shipping.Chain) (string, bool) {
	if !m.Enabled() {
		return "", false
	}
	s, ok := mapSubtypes[m.mode][chain]
	return s, ok
}

// ChainFor refuses a subtype from the other contract: it cannot have come from
// a map call this deployment made.
func (m *Client) ChainFor(subtype string) (shipping.Chain, bool) {
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

// Form holds only parameters ECPay's map documents, so the
// template cannot add one carrying something the shopper typed.
type Form struct {
	Action          string
	MerchantID      string
	MerchantTradeNo string
	LogisticsType   string
	// LogisticsSubType is the chain in the spelling the merchant's contract uses; B2C
	// and C2C spell the same chain differently.
	LogisticsSubType string
	IsCollection     string
	ServerReplyURL   string
	ExtraData        string
	// Device is sent only to a phone, and only 7-ELEVEN reads it.
	Device string
}

// Request builds a form that carries ECPay's own parameters and NOTHING else: the
// checkout's name, phone, e-mail and address must never ride to a third party.
// tradeNo is fresh per render; the nonce travels in ExtraData, which ECPay
// echoes back unchanged.
func (m *Client) Request(chain shipping.Chain, tradeNo, nonce string, mobile bool) (Form, bool) {
	subtype, ok := m.Subtype(chain)
	if !ok {
		return Form{}, false
	}
	f := Form{
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

// NewNonce returns a fresh nonce for the pickup cookie and ExtraData.
func NewNonce() (string, error) {
	b := make([]byte, nonceBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ValidNonce reports whether s has the exact shape NewNonce produces.
func ValidNonce(s string) bool {
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

// CookieName binds the nonce to this origin; development uses a different
// name because a __Host- cookie without Secure is rejected.
const CookieName = "__Host-goen_pickup"

// CookieNameFor is CookieName on a secure deployment and its bare form otherwise.
func CookieNameFor(secure bool) string {
	if secure {
		return CookieName
	}
	return "goen_pickup"
}

// cookieMaxAge bounds how long a nonce left in the address bar is worth
// anything; the map itself times out after three idle minutes.
const cookieMaxAge = 2 * 60 * 60

// State carries the three query keys with the nonce because the return is
// a cross-site POST: the browser sends this Lax cookie on the refreshed GET,
// the only thing that survives the trip.
type State struct {
	Nonce string
	// Ship, Invoice and Address are the three query keys Checkout reads and
	// re-validates, so a tampered cookie chooses nothing a hand-typed URL could
	// not.
	Ship    string
	Invoice string
	Address string
}

// WriteCookie sets the pickup cookie carrying s.
func WriteCookie(w http.ResponseWriter, s State, secure bool) {
	raw := strings.Join([]string{s.Nonce, s.Ship, s.Invoice, s.Address}, "|")
	//nolint:gosec // G124: Secure follows the deployment's own flag, as every cookie here does
	http.SetCookie(w, &http.Cookie{
		Name:     CookieNameFor(secure),
		Value:    base64.RawURLEncoding.EncodeToString([]byte(raw)),
		Path:     "/",
		MaxAge:   cookieMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ReadCookie returns this browser's pickup state, and false when it holds none.
func ReadCookie(r *http.Request, secure bool) (State, bool) {
	// POST state belongs to the submitted form, never to an unrelated query.
	nonce := r.URL.Query().Get("pickup_n")
	if r.Method == http.MethodPost {
		nonce = r.PostFormValue("pickup_n")
	}
	var first State
	var found bool
	for _, c := range r.CookiesNamed(CookieNameFor(secure)) {
		raw, err := base64.RawURLEncoding.DecodeString(c.Value)
		if err != nil {
			continue
		}
		parts := strings.SplitN(string(raw), "|", 4)
		if len(parts) != 4 || !ValidNonce(parts[0]) {
			continue
		}
		state := State{Nonce: parts[0], Ship: parts[1], Invoice: parts[2], Address: parts[3]}
		// Browsers can send same-name cookies from different paths. The one
		// carrying the returned nonce wins; otherwise the first valid cookie is
		// still this browser's state, and Honoured alone decides
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

// NonceMatched reports whether posted equals the nonce of any pickup cookie
// this browser sent; it is for diagnostics, and Honoured alone decides.
func NonceMatched(r *http.Request, secure bool, posted string) bool {
	if !ValidNonce(posted) {
		return false
	}
	matched := false
	for _, c := range r.CookiesNamed(CookieNameFor(secure)) {
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

// ClearCookie expires the pickup cookie: a nonce left behind after an order is
// placed would still honour a store for the next checkout in this browser.
func ClearCookie(w http.ResponseWriter, secure bool) {
	//nolint:gosec // G124: as above
	http.SetCookie(w, &http.Cookie{
		Name: CookieNameFor(secure), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

// PostedStore is no evidence: the map callback carries no signature, and the
// map is run by the chain rather than by ECPay.
type PostedStore struct {
	Chain shipping.Chain
	Code  string
	Name  string
	// Nonce is the only field with weight; the rest is a delivery address a
	// shopper could already type.
	Nonce string
}

func (p PostedStore) Empty() bool {
	return p.Chain == "" && p.Code == "" && p.Name == "" && p.Nonce == ""
}

// Honoured is the ONE rule: a store is honoured only when the returned
// nonce equals the nonce in this browser's own Lax cookie, compared in constant
// time, and the chain is one the checkout offers. The chain equality carries no
// weight of its own: both halves come from the same attacker-influenceable URL.
// It runs on the GET that renders a returned store AND on the POST that places
// the order, otherwise a 422 re-render would carry a dropped store back in its
// hidden fields.
func Honoured(p PostedStore, state State, known bool) bool {
	if !known || !ValidNonce(state.Nonce) || !ValidNonce(p.Nonce) {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(p.Nonce), []byte(state.Nonce)) != 1 {
		return false
	}
	return p.Chain.AtCheckout()
}

// ECPay's published caps for the posted fields; a value over them did not come
// from the map.
const (
	maxCallbackStoreNameRunes = 20
	maxCallbackAddressRunes   = 80
	MaxCallbackBytes          = 8 << 10
)

// Callback is a store the map posted back, of a shape ReadCallback accepted.
type Callback struct {
	Chain   shipping.Chain
	Code    string
	Name    string
	Address string
	Nonce   string
}

// ReadCallback validates SHAPE only: no cookie, no database, and false for
// anything unrecognised rather than repeating it.
func (m *Client) ReadCallback(r *http.Request) (Callback, bool) {
	if !m.Enabled() {
		return Callback{}, false
	}
	if r.PostFormValue("MerchantID") != m.merchantID {
		return Callback{}, false
	}
	chain, ok := m.ChainFor(r.PostFormValue("LogisticsSubType"))
	if !ok {
		return Callback{}, false
	}
	// Uppercased first, as Delivery.Trim does before placement checks the same
	// shape, so the return cannot refuse a code the order accepts.
	code := strings.ToUpper(strings.TrimSpace(r.PostFormValue("CVSStoreID")))
	if !shipping.ValidStoreCode(code) {
		return Callback{}, false
	}
	name := strings.TrimSpace(r.PostFormValue("CVSStoreName"))
	if name == "" || utf8.RuneCountInString(name) > maxCallbackStoreNameRunes || formtext.HasControlChars(name) {
		return Callback{}, false
	}
	address := strings.TrimSpace(r.PostFormValue("CVSAddress"))
	if utf8.RuneCountInString(address) > maxCallbackAddressRunes || formtext.HasControlChars(address) {
		return Callback{}, false
	}
	nonce := r.PostFormValue("ExtraData")
	if !ValidNonce(nonce) {
		return Callback{}, false
	}
	return Callback{Chain: chain, Code: code, Name: name, Address: address, Nonce: nonce}, true
}

// RefreshTarget is the checkout URL the return page sends the browser to. The
// path is a fixed literal and every posted value is one encoded query
// parameter, so nothing in the callback can influence the scheme, host or path.
func (c Callback) RefreshTarget() string {
	q := url.Values{
		"pickup_chain":      {string(c.Chain)},
		"pickup_store_code": {c.Code},
		"pickup_store_name": {c.Name},
		"pickup_store_addr": {c.Address},
		"pickup_n":          {c.Nonce},
	}
	return "/checkout?" + q.Encode()
}
