// Package cart holds goen's cart and checkout.
//
// A cart belongs to a browser before it belongs to an account: guest checkout
// is supported, so the cart is identified by a cookie and adopted on sign-in.
// The cookie carries a random token; the database stores only its SHA-256
// digest, so a leaked table does not hand over live cart cookies.
package cart

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Errors a handler branches on.
var (
	// ErrNotFound is a cart, order or variant that does not exist.
	ErrNotFound = errors.New("cart: not found")
	// ErrUnavailable is a variant that cannot be added or ordered: inactive,
	// its product not active, or not enough sellable stock.
	ErrUnavailable = errors.New("cart: variant unavailable")
	// ErrEmpty is a checkout with nothing in the cart.
	ErrEmpty = errors.New("cart: empty")
)

// PlacedCookieName carries the browser's proof that it placed an order.
//
// It holds TOKENS, not order numbers, because a number in here would BE the proof
// and numbers come off a per-day counter (GO-260803-000001, then 000002). Anybody
// could set such a cookie by hand, increment it, and read a stranger's email,
// address and items, then cancel the order, start a payment or open a return.
//
// `__Host-`, Secure, HttpOnly and SameSite all govern how a BROWSER treats a cookie.
// None of them says the value came from this server, and curl does not have to care.
// The token is high-entropy and the server keeps only its digest, the same shape
// sessions, reset tokens and cart tokens already use.
const PlacedCookieName = "__Host-goen_placed"

// maxRememberedOrders bounds the list, because the cookie travels on every request.
// Enough for a session's worth of ordering.
const maxRememberedOrders = 10

// RememberOrder issues a token for an order and adds it to the browser's list.
//
// The grant is written BEFORE the cookie is set: a cookie naming a token this
// database does not know is a customer locked out of their own order, while a grant
// with no cookie is a row nobody can use — one is a support ticket and the other is
// nothing.
func (s *Store) RememberOrder(
	ctx context.Context, w http.ResponseWriter, r *http.Request, number string, secure bool,
) error {
	token, err := NewToken()
	if err != nil {
		return err
	}
	n, err := s.q.GrantOrderAccess(ctx, db.GrantOrderAccessParams{
		Digest: HashToken(token), OrderNumber: number,
	})
	if err != nil {
		return fmt.Errorf("grant access to order %s: %w", number, err)
	}
	if n == 0 {
		// The INSERT ... SELECT matched no order, which SQL does not call an
		// error. Without this the caller would set a cookie carrying a token no
		// grant backs — a customer locked out of their own order with no failure
		// anywhere to explain it.
		return fmt.Errorf("grant access to order %s: %w", number, ErrNotFound)
	}

	// The cookie about to be written carries the OLDER tokens forward with a
	// fresh MaxAge, so the grants behind them need their clock restarted on the
	// same event — see TouchOrderAccessGrants.
	//
	// Its failure must NOT skip the cookie below. Returning early here would cost
	// the customer the order they just placed, to avoid a lockout 30 days away
	// that /orders/find already answers — so the cookie is written either way and
	// the error is reported afterwards, where rememberOrder logs it without
	// failing a completed checkout.
	var touchErr error
	if carried := placedTokens(r, secure); len(carried) > 0 {
		digests := make([][]byte, 0, len(carried))
		for _, t := range carried {
			digests = append(digests, HashToken(t))
		}
		if err := s.q.TouchOrderAccessGrants(ctx, digests); err != nil {
			touchErr = fmt.Errorf("refresh carried order access grants: %w", err)
		}
	}

	writePlacedCookie(w, r, token, secure)
	return touchErr
}

// writePlacedCookie puts a token at the front of the browser's list.
func writePlacedCookie(w http.ResponseWriter, r *http.Request, token string, secure bool) {
	tokens := append([]string{token}, placedTokens(r, secure)...)
	seen := make(map[string]bool, len(tokens))
	kept := make([]string, 0, maxRememberedOrders)
	for _, t := range tokens {
		if seen[t] || t == "" {
			continue
		}
		seen[t] = true
		kept = append(kept, t)
		if len(kept) == maxRememberedOrders {
			break
		}
	}
	// G124 is suppressed because Secure is a VARIABLE: it is false only under
	// GOEN_INSECURE_COOKIES in development, where there is no TLS to mark. The
	// __Host- prefix in the production name enforces the rest.
	//nolint:gosec // G124: Secure is set from the deployment's own flag, below
	http.SetCookie(w, &http.Cookie{
		Name:     placedCookieName(secure),
		Value:    strings.Join(kept, "."),
		Path:     "/",
		MaxAge:   cookieMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// placedTokens is the tokens this browser is carrying.
func placedTokens(r *http.Request, secure bool) []string {
	c, err := r.Cookie(placedCookieName(secure))
	if err != nil || c.Value == "" {
		return nil
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) > maxRememberedOrders {
		parts = parts[:maxRememberedOrders]
	}
	return parts
}

// PlacedHere reports whether this browser holds a token for the named order.
//
// The comparison happens in the DATABASE, against digests, and the answer is a
// boolean — the same shape FindOrder returns and for the same reason: a caller that
// got rows back could say WHICH token matched, and which orders a browser can reach
// is not something any page needs to disclose.
//
// An error is NOT access. A database that cannot answer has not said yes.
func (s *Store) PlacedHere(ctx context.Context, r *http.Request, number string, secure bool) bool {
	tokens := placedTokens(r, secure)
	if len(tokens) == 0 || number == "" {
		return false
	}
	digests := make([][]byte, 0, len(tokens))
	for _, t := range tokens {
		digests = append(digests, HashToken(t))
	}
	ok, err := s.q.OrderAccessibleWith(ctx, db.OrderAccessibleWithParams{
		OrderNumber: number, Digests: digests,
	})
	if err != nil {
		return false
	}
	return ok
}

func placedCookieName(secure bool) string {
	if secure {
		return PlacedCookieName
	}
	return "goen_placed"
}

// CookieName is the cart cookie. The __Host- prefix binds it to this exact
// origin with no Domain attribute and requires Secure, which is what stops a
// sibling subdomain from writing a cart cookie the storefront would then trust.
const CookieName = "__Host-goen_cart"

// cookieMaxAge is how long an abandoned cart survives. Long enough to come back
// to tomorrow, short enough that a shared browser does not surface someone
// else's cart weeks later.
const cookieMaxAge = 30 * 24 * 60 * 60 // 30 days

// tokenBytes is the token's entropy. 32 bytes is well past guessing, and the
// token is the only thing standing between a stranger and a cart.
const tokenBytes = 32

// MaxLineQuantity is the most of one variant a cart may hold, matching the
// schema's own CHECK.
const MaxLineQuantity = 999

// PayWindow is how long after PLACING an order a customer may still start
// paying for it.
//
// It is not the same quantity as [HoldTTL], and conflating them — thirty
// minutes each — breaks payment ENTIRELY. The hold is stamped at PlaceOrder and
// the payment page asks whether enough of it is left to open a Checkout Session
// against; Stripe will not accept one expiring less than [StripeSessionFloor]
// out. With HoldTTL equal to that floor the question reduces to
// `placed_at >= pay_at`, which is false the instant after checkout — so no
// session can ever be created and no order can ever be paid for.
//
// Two durations measured from DIFFERENT instants are not the same window — the
// lesson [Order.SessionExpiry] records one level up, about the session and the
// hold. The same trap sits in these two constants underneath it.
const PayWindow = 30 * time.Minute

// StripeSessionFloor mirrors payment.MinSessionLifetime, which is Stripe's own
// minimum for a Checkout Session's expires_at.
//
// Mirrored rather than imported: internal/payment already reaches into this
// package through a consumer-defined interface, and one number is a poor reason
// to point the dependency back the other way.
// TestAPlacedOrderCanActuallyBePaidFor binds the two through the real gate and
// the real sequence — a hold stamped at placement and read at a LATER instant.
// That sequence is the lock: a test that reads the hold at the instant it was
// stamped never reaches the comparison this constant decides.
const StripeSessionFloor = 30 * time.Minute

// HoldTTL is how long an order's stock is reserved while payment is attempted.
//
// It is DERIVED, not chosen: a customer who reaches the pay page at the last
// moment of [PayWindow] must still leave behind a hold long enough for Stripe to
// accept a session, and that session then expires with the hold rather than
// after it. Writing it as a sum is what stops the two ends drifting into the
// equality that leaves every order unpayable — see [PayWindow].
//
// The trade-off inside PayWindow is the real one. Too short and a customer
// typing a card number loses the item under them; too long and an abandoned
// checkout keeps stock off the shelf. inventory_reservations carries expires_at
// so a sweeper can return what was never paid for.
const HoldTTL = PayWindow + StripeSessionFloor

// NewToken returns a fresh cart token. crypto/rand, never math/rand: a
// predictable token is a readable cart.
func NewToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken digests a token for storage and lookup. The database never sees the
// token itself.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// SetCookie writes the cart cookie.
//
// secure is false only in development over plain HTTP; the __Host- prefix
// requires Secure, so the name is adjusted rather than the guarantee quietly
// dropped — a cookie that claims __Host- without Secure is rejected by the
// browser and the cart would silently never persist.
func SetCookie(w http.ResponseWriter, token string, secure bool) {
	// gosec flags Secure=false. That is the development path and it is
	// deliberate: a Secure cookie is never returned over the plain http:// the
	// dev server speaks, so the cart would appear to lose itself on every
	// request. The default is secure — GOEN_INSECURE_COOKIES=1 is what opts out
	// — so a deployment that forgets fails closed.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: dev-only opt-out, secure by default
		Name:     cookieName(secure),
		Value:    token,
		Path:     "/",
		MaxAge:   cookieMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ReadCookie returns the token a request carries, or "".
func ReadCookie(r *http.Request, secure bool) string {
	c, err := r.Cookie(cookieName(secure))
	if err != nil {
		return ""
	}
	return c.Value
}

// cookieName is the __Host- form when the connection can carry it, and a plain
// name in development.
func cookieName(secure bool) string {
	if secure {
		return CookieName
	}
	return "goen_cart"
}

// ParseQuantity reads a quantity from a form, clamped to what a line may hold.
// Anything unparseable is one item: the visitor pressed a button meaning "add
// this", and refusing over a malformed number they never typed helps nobody.
func ParseQuantity(s string) int32 {
	// ParseInt with bitSize 32 rather than Atoi: the API guarantees the result
	// fits an int32, so there is no narrowing conversion to prove safe.
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
	switch {
	case err != nil, n < 1:
		return 1
	case n > MaxLineQuantity:
		return MaxLineQuantity
	}
	return int32(n)
}

// ParseQuantityAllowingZero is the cart page's version: zero is a real answer
// there, meaning remove this line.
func ParseQuantityAllowingZero(s string) (int32, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
	switch {
	case err != nil, n < 0:
		return 0, false
	case n > MaxLineQuantity:
		return MaxLineQuantity, true
	}
	return int32(n), true
}

// Destination is where a shipping method delivers to, and therefore what the
// checkout must ask for. It comes from shipping_methods.destination_kind — the
// schema decides, not a branch here on a method code.
type Destination string

// The destinations goen knows. A method with any other kind is unroutable, and
// DestinationFor says so rather than guessing.
const (
	// ToAddress wants a street address: 宅配到府.
	ToAddress Destination = "address"
	// ToPickupPoint wants a convenience store: 超商取貨. A street address for
	// one of these is data nobody will ever use.
	ToPickupPoint Destination = "pickup_point"
)

// DestinationFor turns the column's value into a Destination.
//
// An unknown kind is refused rather than defaulted to ToAddress. Defaulting
// would mean a method added later, with a destination nothing here understands,
// silently collects a street address and produces orders no one can deliver —
// and the schema's CHECK would let it, because a street address is a valid
// destination for something.
func DestinationFor(kind string) (Destination, bool) {
	switch Destination(kind) {
	case ToAddress:
		return ToAddress, true
	case ToPickupPoint:
		return ToPickupPoint, true
	}
	return "", false
}

// Address is the delivery detail a checkout collects.
//
// It carries BOTH destinations and a Destination saying which one applies,
// rather than being two types. The contact half — email, name, phone — is the
// same for either, and splitting the type would duplicate it along with every
// rule that validates it.
type Address struct {
	// To decides which half of this struct is real. Set from the chosen
	// shipping method, never from the form: a hidden field naming the
	// destination would let a hand-edited submission attach a street address to
	// a pickup order, which the schema then refuses at the very end of a
	// checkout instead of at its start.
	To Destination

	Email string
	Name  string
	Phone string

	// The street address. Empty unless To is ToAddress.
	PostalCode string
	City       string
	District   string
	Street     string

	// The convenience store. Empty unless To is ToPickupPoint.
	PickupBrand     string
	PickupStoreCode string
	PickupStoreName string

	Note string
}

// FieldError names one rejected field and why.
//
// The reason is a message KEY rather than a sentence. Validation runs where
// there is no request and therefore no locale — Validate is called from a
// handler, a test and a store — so the words are chosen at render time by
// whoever knows who is reading.
type FieldError struct {
	Field      string
	MessageKey i18n.Key
}

// maxima for the free-text fields. The schema does not cap them; these keep a
// form submission from becoming an unbounded row.
const (
	maxNameRunes   = 60
	maxStreetRunes = 200
	// A 門市名稱 is short — 「台北車站門市」 and the like. The bound is what
	// keeps a paste into the field from becoming an unbounded row.
	maxStoreNameRunes = 40
	maxNoteRunes      = 500
	maxEmailRunes     = 254
	// A 門市代碼 as the chains publish it: six digits at 7-ELEVEN, 全家 and OK,
	// four characters at 萊爾富. The bound is the one ECPay states for the field
	// rather than any chain's own width, so a chain that renumbers does not make
	// this line refuse a real store.
	maxStoreCodeLen = 10
)

// Validate checks an address the way the server must: completely, and before
// anything is written. The browser's required and type=email are the first
// line, never the only one.
//
// Errors are returned in field order so the form's messages appear where the
// eye already is, rather than in whatever order the checks happened to run.
func (a *Address) Validate() []FieldError {
	var errs []FieldError
	add := func(f string, k i18n.Key) { errs = append(errs, FieldError{Field: f, MessageKey: k}) }

	if k := emailError(a.Email); k != "" {
		add("email", k)
	}

	if strings.TrimSpace(a.Name) == "" {
		add("name", i18n.KeyNameRequired)
	} else if len([]rune(a.Name)) > maxNameRunes {
		add("name", i18n.KeyNameTooLong)
	}

	// Taiwanese mobile and landline numbers, digits and separators only. Kept
	// deliberately loose: rejecting a real number is worse than accepting an
	// odd one, and the courier is the real validator.
	switch {
	case strings.TrimSpace(a.Phone) == "":
		add("phone", i18n.KeyPhoneRequired)
	case !looksLikePhone(a.Phone):
		add("phone", i18n.KeyPhoneMalformed)
	}

	errs = append(errs, a.destinationErrors()...)

	if len([]rune(a.Note)) > maxNoteRunes {
		add("note", i18n.KeyNoteTooLong)
	}

	return append(errs, a.controlCharErrors()...)
}

// destinationErrors validates the half of the struct that applies.
//
// The switch has no permissive default: a Destination nothing here recognises
// means the method's destination_kind was never taught to this code, and
// letting such an order through would write a row with no destination at all.
func (a *Address) destinationErrors() []FieldError {
	var errs []FieldError
	add := func(f string, k i18n.Key) { errs = append(errs, FieldError{Field: f, MessageKey: k}) }

	switch a.To {
	case ToAddress:
		if !isPostalCode(a.PostalCode) {
			add("postal_code", i18n.KeyPostalCodeMalformed)
		}
		if strings.TrimSpace(a.City) == "" {
			add("city", i18n.KeyCityRequired)
		}
		if strings.TrimSpace(a.District) == "" {
			add("district", i18n.KeyDistrictRequired)
		}
		switch {
		case strings.TrimSpace(a.Street) == "":
			add("street", i18n.KeyStreetRequired)
		case len([]rune(a.Street)) > maxStreetRunes:
			add("street", i18n.KeyStreetTooLong)
		}
	case ToPickupPoint:
		// Validated against the list the FORM offers, so a brand the page shows
		// and the server refuses cannot exist. The authority for what may be
		// stored is order_private_data_pickup_brand_known; this list is the one
		// both the control and this check read.
		if !slices.Contains(pages.PickupBrands, a.PickupBrand) {
			add("pickup_brand", i18n.KeyPickupBrandRequired)
		}
		if !isStoreCode(a.PickupStoreCode) {
			add("pickup_store_code", i18n.KeyStoreCodeMalformed)
		}
		switch {
		case strings.TrimSpace(a.PickupStoreName) == "":
			add("pickup_store_name", i18n.KeyStoreNameRequired)
		case len([]rune(a.PickupStoreName)) > maxStoreNameRunes:
			add("pickup_store_name", i18n.KeyStoreNameTooLong)
		}
	default:
		add("shipping", i18n.KeyChooseShipping)
	}
	return errs
}

// ForDestination blanks the half of the address that does not apply.
//
// Called after validation and before the write, so a customer who filled the
// address fields, switched to 超商取貨 and submitted does not leave a home
// address on a pickup order — order_private_data_one_destination would refuse
// it, and refusing at the end of a checkout is the wrong place to find out.
func (a *Address) ForDestination() {
	switch a.To {
	case ToAddress:
		a.PickupBrand, a.PickupStoreCode, a.PickupStoreName = "", "", ""
	case ToPickupPoint:
		a.PostalCode, a.City, a.District, a.Street = "", "", "", ""
	}
}

// isStoreCode reports whether s is a convenience-store number.
//
// Digits or uppercase letters, and never digits alone. "A store code is always
// a number" is a guess, and it is wrong: 萊爾富 numbers its stores in four
// characters and 149 of its 1,350 lead with a letter, so a digits-only rule
// refuses every one of them at checkout and offers no way through. The
// measurement is recorded beside order_private_data_pickup_store_code_format,
// which is the authority; this is the copy that answers with a field name rather
// than a constraint violation at the end of a checkout.
//
// The wider class still catches what this rule is for — a 店名 typed into the
// code field is Han text, which is in neither class.
//
// It is an EXACT mirror of that CHECK, deliberately: it neither trims nor folds
// case. A validator looser than the schema accepts a value the write then
// refuses, which moves the refusal to the end of a checkout. [Address.Trim] is
// where lowercase input is uppercased, and is what makes s884 reach here as
// S884.
func isStoreCode(s string) bool {
	if s == "" || len(s) > maxStoreCodeLen {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

// controlCharErrors reports any field carrying a control character. They are
// invisible, and a newline in a name is how a shipping label or a confirmation
// email gets a line it was never given.
func (a *Address) controlCharErrors() []FieldError {
	var errs []FieldError
	for _, f := range []struct{ name, value string }{
		{"email", a.Email}, {"name", a.Name}, {"phone", a.Phone},
		{"postal_code", a.PostalCode}, {"city", a.City},
		{"district", a.District}, {"street", a.Street},
		{"pickup_brand", a.PickupBrand}, {"pickup_store_code", a.PickupStoreCode},
		{"pickup_store_name", a.PickupStoreName}, {"note", a.Note},
	} {
		if hasControl(f.value) {
			errs = append(errs, FieldError{Field: f.name, MessageKey: i18n.KeyFieldHasControlChars})
		}
	}
	return errs
}

// emailError returns why an address is unusable, or "".
func emailError(s string) i18n.Key {
	switch {
	case strings.TrimSpace(s) == "":
		return i18n.KeyCheckoutEmailRequired
	case len([]rune(s)) > maxEmailRunes:
		return i18n.KeyCheckoutEmailTooLong
	case !looksLikeEmail(s):
		return i18n.KeyCheckoutEmailMalformed
	}
	return ""
}

func looksLikeEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 || strings.Count(s, "@") != 1 {
		return false
	}
	domain := s[at+1:]
	dot := strings.IndexByte(domain, '.')
	return dot > 0 && dot < len(domain)-1 && !strings.ContainsAny(s, " \t")
}

func looksLikePhone(s string) bool {
	digits := 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '-' || r == ' ' || r == '(' || r == ')' || r == '+':
		default:
			return false
		}
	}
	return digits >= 8 && digits <= 15
}

func isPostalCode(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 3 || len(s) > 6 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// hasControl reports whether s carries a control character.
//
// unicode.IsControl covers BOTH the C0 range (0x00–0x1F, 0x7F) and C1
// (0x80–0x9F) — the second matters because C1 characters are invisible and are
// the classic way past a check that only looks at ASCII. Verified rather than
// assumed: an explicit C1 branch here would be dead code, because IsControl
// already returns true for U+0085 and its neighbours. U+00A0, the non-breaking
// space, is correctly NOT a control character and stays allowed.
func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// Trim normalises what a form inevitably carries: the whitespace around every
// field, and the CASE of a store code.
//
// 萊爾富's codes are uppercase — S884 — and somebody who types s884 has named
// the right store. Refusing that would make this validator stricter than the
// thing it models, over a shift key.
func (a *Address) Trim() {
	a.Email = strings.TrimSpace(a.Email)
	a.Name = strings.TrimSpace(a.Name)
	a.Phone = strings.TrimSpace(a.Phone)
	a.PostalCode = strings.TrimSpace(a.PostalCode)
	a.City = strings.TrimSpace(a.City)
	a.District = strings.TrimSpace(a.District)
	a.Street = strings.TrimSpace(a.Street)
	a.PickupBrand = strings.TrimSpace(a.PickupBrand)
	a.PickupStoreCode = strings.ToUpper(strings.TrimSpace(a.PickupStoreCode))
	a.PickupStoreName = strings.TrimSpace(a.PickupStoreName)
	a.Note = strings.TrimSpace(a.Note)
}

// ShippingFee is what a shipping version charges for an order of this subtotal.
// A free-over threshold of zero means the method is never free.
func ShippingFee(feeCents, freeOverCents, subtotalCents int64) int64 {
	if freeOverCents > 0 && subtotalCents >= freeOverCents {
		return 0
	}
	return feeCents
}

// Quote is what an order actually pays to be delivered.
//
// The surcharge is added AFTER the free-over threshold, and that ordering is
// the commercial decision: 免運 is the shop's offer on its own base rate, and
// the carrier still charges to cross the water. Folding the surcharge into the
// threshold would make a NT$5,000 order to 金門 free to send, which it is not.
type Quote struct {
	FeeCents  int64
	Surcharge int64
	// ZoneName is what to call the surcharge on the page: 「離島加價」 rather
	// than an unexplained larger number.
	ZoneName string
}

// Total is what the order is charged for delivery.
func (q Quote) Total() int64 { return q.FeeCents + q.Surcharge }

// HasSurcharge reports whether this address costs extra to reach.
func (q Quote) HasSurcharge() bool { return q.Surcharge > 0 }

// Invoice is what a customer wants on their 統一發票.
//
// Taiwan e-invoices go to a carrier (載具) or to a company's 統編, and the
// choice changes which other field is required. goen COLLECTS this at checkout;
// issuing the document itself needs a 加值中心 integration that does not exist
// yet, and the schema's invoice_documents is waiting for it.
type Invoice struct {
	// Type is 'mobile_carrier', 'member_carrier' or 'company', matching
	// invoice_preferences_type_known.
	Type string
	// Carrier is the 手機條碼載具: a slash and seven characters from a fixed
	// alphabet. Only meaningful for mobile_carrier.
	Carrier string
	// TaxID is the eight-digit 統一編號. Only meaningful for company.
	TaxID string
}

// InvoiceTypes is every choice the form offers, in the order it offers them.
//
// Derived from nothing — it IS the list, and invoice_preferences_type_known is
// the same list in the schema. A value outside it is refused here so the
// customer gets a message rather than a constraint violation.
var InvoiceTypes = []string{"member_carrier", "mobile_carrier", "company"}

// mobileCarrier is the 手機條碼 format the Ministry of Finance issues: a slash
// followed by seven characters drawn from digits, capitals, and + - . only.
var mobileCarrier = regexp.MustCompile(`^/[0-9A-Z+\-.]{7}$`)

// taxID is the eight-digit 統一編號, matching the schema's own CHECK.
// invoice_preferences_company_has_tax_id's own CHECK, so the two can be
// compared by eye. \d would also admit non-ASCII digits under some engines.
//
//nolint:gocritic // regexpSimplify: kept character-for-character identical to
var taxID = regexp.MustCompile(`^[0-9]{8}$`)

// Validate refuses what the schema would refuse, in the customer's language.
//
// An empty Type is not an error: it means the customer left the default, which
// is a member carrier. Defaulting here rather than in the template keeps the
// rule in one place.
func (i *Invoice) Validate() []FieldError {
	i.Type = strings.TrimSpace(i.Type)
	i.Carrier = strings.ToUpper(strings.TrimSpace(i.Carrier))
	i.TaxID = strings.TrimSpace(i.TaxID)

	if i.Type == "" {
		i.Type = "member_carrier"
	}
	if !slices.Contains(InvoiceTypes, i.Type) {
		return []FieldError{{Field: "invoice_type", MessageKey: i18n.KeyInvoiceTypeRequired}}
	}

	var errs []FieldError
	switch i.Type {
	case "mobile_carrier":
		if !mobileCarrier.MatchString(i.Carrier) {
			errs = append(errs, FieldError{
				Field:      "invoice_carrier",
				MessageKey: i18n.KeyCarrierMalformed,
			})
		}
		i.TaxID = ""
	case "company":
		if !taxID.MatchString(i.TaxID) {
			errs = append(errs, FieldError{
				Field:      "invoice_tax_id",
				MessageKey: i18n.KeyTaxIDMalformed,
			})
		}
		i.Carrier = ""
	default: // member_carrier keeps neither
		i.Carrier, i.TaxID = "", ""
	}
	return errs
}

// InvoiceTypeLabelKey names the message for one choice.
//
// The types are invoice_preferences_type_known's CHECK. An unknown one is a
// schema change nobody carried through here, which must be loud.
func InvoiceTypeLabelKey(t string) i18n.Key {
	switch t {
	case "member_carrier":
		return i18n.KeyInvoiceMember
	case "mobile_carrier":
		return i18n.KeyInvoiceMobile
	case "company":
		return i18n.KeyInvoiceCompany
	default:
		panic("cart: no label for invoice type " + t)
	}
}
