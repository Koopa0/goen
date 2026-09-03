// Package cart holds goen's cart and checkout. A cart is identified by a cookie
// whose random token the database stores only as a SHA-256 digest.
package cart

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	invoicepkg "github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/pickup"
)

var (
	// ErrNotFound is a cart, order or variant that does not exist.
	ErrNotFound = errors.New("cart: not found")
	// ErrUnavailable is a variant that cannot be added or ordered.
	ErrUnavailable = errors.New("cart: variant unavailable")
	// ErrCreditChanged means the customer's available store credit moved while
	// checkout was being placed. The form must show the fresh figure before a
	// second submission.
	ErrCreditChanged = errors.New("cart: available store credit changed")
	// ErrEmpty is a checkout with nothing in the cart.
	ErrEmpty = errors.New("cart: empty")
	// ErrTooManyItems means the cart has no room for another distinct product
	// while keeping every resulting invoice within ECPay's Items limit.
	ErrTooManyItems = errors.New("cart: too many invoice items")
	// errCheckoutChanged means the commercial facts no longer match the quote the
	// customer confirmed. The refreshed quote must be shown before retrying.
	errCheckoutChanged = errors.New("cart: checkout quote changed")
	// errCheckoutKeyConflict is an idempotency key already owned by another
	// cart. It stays private: Handler replaces it rather than exposing whether a
	// guessed key exists.
	errCheckoutKeyConflict = errors.New("cart: checkout key belongs to another cart")
	// errCheckoutMoney is an impossible commercial total. Browser input never
	// supplies money, so reaching it means server-owned state cannot be represented
	// safely as the int64 cents an order stores.
	errCheckoutMoney = errors.New("cart: checkout money is out of range")
)

// checkoutQuoteLine is one exact item-and-price fact in a checkout quote.
type checkoutQuoteLine struct {
	VariantID uuid.UUID
	Quantity  int32
	UnitCents int64
}

// checkoutQuote represents the exact commercial facts a customer confirmed.
// It is not persisted and its ID is not an authorization credential: checkout
// rebuilds the same facts from locked database rows and accepts only equality.
type checkoutQuote struct {
	CartID uuid.UUID
	Lines  []checkoutQuoteLine

	ShippingVersionID uuid.UUID
	ShippingCents     int64

	CouponCode    string
	DiscountCents int64
	CreditCents   int64
}

// checkoutQuoteID is the fixed-size identity of one canonical checkoutQuote.
// A named array prevents arbitrary strings from reaching Store.placeOrder.
type checkoutQuoteID [sha256.Size]byte

const checkoutAttemptIDBytes = 16

// checkoutAttemptID is the server-issued identity of one checkout attempt. A
// fixed array keeps arbitrary form text out of the store and database writer;
// only the HTTP boundary and test-only facades parse its canonical wire form.
type checkoutAttemptID [checkoutAttemptIDBytes]byte

// newCheckoutAttemptID returns a fresh non-zero checkout identity.
func newCheckoutAttemptID() (checkoutAttemptID, error) {
	var id checkoutAttemptID
	if _, err := rand.Read(id[:]); err != nil {
		return checkoutAttemptID{}, fmt.Errorf("generate checkout attempt ID: %w", err)
	}
	if id == (checkoutAttemptID{}) {
		return checkoutAttemptID{}, errors.New("generate checkout attempt ID: random source returned zero")
	}
	return id, nil
}

// parseCheckoutAttemptID accepts only the canonical 22-character RawURL form
// emitted by checkoutAttemptID.String.
func parseCheckoutAttemptID(value string) (checkoutAttemptID, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != checkoutAttemptIDBytes {
		return checkoutAttemptID{}, errors.New("cart: malformed checkout attempt ID")
	}
	var id checkoutAttemptID
	copy(id[:], decoded)
	if id == (checkoutAttemptID{}) || id.String() != value {
		return checkoutAttemptID{}, errors.New("cart: malformed checkout attempt ID")
	}
	return id, nil
}

// String is the checkout attempt identity's unpadded URL-safe form.
func (id checkoutAttemptID) String() string {
	return base64.RawURLEncoding.EncodeToString(id[:])
}

// ID validates q and returns its order-independent canonical identity. Lines
// are copied before sorting, so calculating an ID never mutates caller state.
func (q checkoutQuote) ID() (checkoutQuoteID, error) {
	lines, code, err := q.identityFacts()
	if err != nil {
		return checkoutQuoteID{}, err
	}

	encoded := make([]byte, 0, 128+len(lines)*28+len(code))
	encoded = append(encoded, "goen-checkout-quote\x00v1\x00"...)
	encoded = append(encoded, q.CartID[:]...)
	// identityFacts bounds the slice length to the wire format's uint32 field.
	encoded = binary.BigEndian.AppendUint32(encoded, uint32(len(lines))) //nolint:gosec // proven bounded
	for i := range lines {
		line := &lines[i]
		encoded = append(encoded, line.VariantID[:]...)
		// identityFacts accepts only positive int32 quantities and non-negative
		// int64 prices, both exactly representable by the unsigned wire fields.
		encoded = binary.BigEndian.AppendUint32(encoded, uint32(line.Quantity))  //nolint:gosec // proven non-negative
		encoded = binary.BigEndian.AppendUint64(encoded, uint64(line.UnitCents)) //nolint:gosec // proven non-negative
	}
	encoded = append(encoded, q.ShippingVersionID[:]...)
	// identityFacts rejects every negative money field before encoding.
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(q.ShippingCents)) //nolint:gosec // proven non-negative
	// couponCode admits at most 32 ASCII bytes, so this conversion cannot truncate.
	encoded = binary.BigEndian.AppendUint32(encoded, uint32(len(code))) //nolint:gosec // regex-bounded to 32
	encoded = append(encoded, code...)
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(q.DiscountCents)) //nolint:gosec // proven non-negative
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(q.CreditCents))   //nolint:gosec // proven non-negative
	return sha256.Sum256(encoded), nil
}

// identityFacts validates the quote's domain constraints and returns the two
// canonical variable-width fields used by ID. Keeping validation separate from
// encoding makes the proof for each unsigned wire conversion explicit.
func (q checkoutQuote) identityFacts() (lines []checkoutQuoteLine, code string, err error) {
	code, err = q.validateIdentityHeader()
	if err != nil {
		return nil, "", err
	}

	lines = slices.Clone(q.Lines)
	slices.SortFunc(lines, func(a, b checkoutQuoteLine) int {
		return bytes.Compare(a.VariantID[:], b.VariantID[:])
	})
	if uint64(len(lines)) > math.MaxUint32 {
		return nil, "", errors.New("cart: checkout quote contains too many lines")
	}
	subtotal, err := checkoutQuoteSubtotal(lines)
	if err != nil {
		return nil, "", err
	}
	if err := q.validateIdentityTotal(subtotal); err != nil {
		return nil, "", err
	}
	return lines, code, nil
}

func (q checkoutQuote) validateIdentityHeader() (code string, err error) {
	if q.CartID == uuid.Nil || q.ShippingVersionID == uuid.Nil || len(q.Lines) == 0 {
		return "", errors.New("cart: incomplete checkout quote")
	}
	if q.ShippingCents < 0 || q.DiscountCents < 0 || q.CreditCents < 0 {
		return "", errors.New("cart: checkout quote contains negative money")
	}

	code = NormaliseCode(q.CouponCode)
	if code != q.CouponCode || (code != "" && !couponCode.MatchString(code)) {
		return "", errors.New("cart: checkout quote contains a non-canonical coupon code")
	}
	if code == "" && q.DiscountCents != 0 {
		return "", errors.New("cart: checkout quote discounts without a coupon")
	}
	return code, nil
}

func (q checkoutQuote) validateIdentityTotal(subtotal int64) error {
	gross, err := checkoutGross(subtotal, q.ShippingCents, q.DiscountCents)
	if err != nil {
		return fmt.Errorf("cart: checkout quote total is invalid: %w", err)
	}
	if q.CreditCents > gross {
		return errors.New("cart: checkout quote credit exceeds its total")
	}
	return nil
}

func checkoutQuoteSubtotal(lines []checkoutQuoteLine) (int64, error) {
	var subtotal int64
	for i := range lines {
		line := lines[i]
		if line.VariantID == uuid.Nil || line.Quantity <= 0 || line.UnitCents < 0 {
			return 0, errors.New("cart: checkout quote contains an invalid line")
		}
		if i > 0 && lines[i-1].VariantID == line.VariantID {
			return 0, errors.New("cart: checkout quote contains a duplicate variant")
		}
		var err error
		subtotal, err = addCheckoutLine(subtotal, line.UnitCents, line.Quantity)
		if err != nil {
			return 0, fmt.Errorf("cart: checkout quote subtotal: %w", err)
		}
	}
	return subtotal, nil
}

// addCheckoutLine adds one non-negative unit-price/quantity product without
// allowing multiplication or accumulation to wrap.
func addCheckoutLine(subtotal, unitCents int64, quantity int32) (int64, error) {
	if subtotal < 0 || unitCents < 0 || quantity <= 0 {
		return 0, errCheckoutMoney
	}
	count := int64(quantity)
	if unitCents > math.MaxInt64/count {
		return 0, errCheckoutMoney
	}
	return addCheckoutMoney(subtotal, unitCents*count)
}

// addCheckoutMoney adds two non-negative cent amounts without wrapping int64.
func addCheckoutMoney(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, errCheckoutMoney
	}
	return a + b, nil
}

// checkoutGross is the one checked definition of subtotal + shipping -
// discount used by both the rendered quote identity and locked placement.
func checkoutGross(subtotal, shipping, discount int64) (int64, error) {
	beforeDiscount, err := addCheckoutMoney(subtotal, shipping)
	if err != nil || discount < 0 || discount > beforeDiscount {
		return 0, errCheckoutMoney
	}
	return beforeDiscount - discount, nil
}

// String is the unpadded URL-safe representation carried by the checkout form.
func (id checkoutQuoteID) String() string {
	return base64.RawURLEncoding.EncodeToString(id[:])
}

// parseCheckoutQuoteID accepts only the canonical fixed-length form encoding.
func parseCheckoutQuoteID(value string) (checkoutQuoteID, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return checkoutQuoteID{}, errors.New("cart: malformed checkout quote ID")
	}
	var id checkoutQuoteID
	copy(id[:], decoded)
	if id == (checkoutQuoteID{}) || id.String() != value {
		return checkoutQuoteID{}, errors.New("cart: malformed checkout quote ID")
	}
	return id, nil
}

// PlacedCookieName carries the browser's proof that it placed an order. It holds
// high-entropy tokens and never order numbers, which are guessable.
const PlacedCookieName = "__Host-goen_placed"

const maxRememberedOrders = 10

// RememberOrder issues a token for an order and adds it to the browser's list.
// The grant is written first: a cookie naming a token this database does not
// know locks the customer out of their own order.
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
		// The INSERT ... SELECT matched no order, which SQL does not call an error.
		return fmt.Errorf("grant access to order %s: %w", number, ErrNotFound)
	}

	// The cookie below carries older tokens forward with a fresh MaxAge, so their
	// grants need the same retention clock restarted.
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
// origin, so a sibling subdomain cannot write a cart cookie goen would trust.
const CookieName = "__Host-goen_cart"

const cookieMaxAge = 30 * 24 * 60 * 60 // 30 days

const tokenBytes = 32

// MaxLineQuantity is the most of one variant a cart may hold, matching the
// schema's own CHECK.
const MaxLineQuantity = 999

// payWindow is how long after placing an order a customer may still start a new
// Checkout Session. It leaves the public 60-minute stock hold room for Stripe's
// 30-minute floor plus the creation margin below.
const payWindow = 29 * time.Minute

// stripeSessionFloor is Stripe's minimum for a Checkout Session's expires_at.
const stripeSessionFloor = 30 * time.Minute

// stripeSessionStartMargin absorbs Unix
// second truncation, clock skew and the request trip to Stripe; without it the
// advertised end of payWindow is already too late to create a session.
const stripeSessionStartMargin = time.Minute

// holdTTL is how long an order's stock is reserved while payment is attempted.
// The floor and creation margin are inventory time, not extra customer pay time:
// they let a session started at payWindow expire with the same stock hold.
const holdTTL = payWindow + stripeSessionFloor + stripeSessionStartMargin

// NewToken returns a fresh cart token.
func NewToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken digests a token for storage and lookup.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// SetCookie writes the cart cookie. A cookie claiming __Host- without Secure is
// rejected by the browser, so the development path uses a different name.
func SetCookie(w http.ResponseWriter, token string, secure bool) {
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

func cookieName(secure bool) string {
	if secure {
		return CookieName
	}
	return "goen_cart"
}

// ParseQuantity reads a quantity from a form, clamped to what a line may hold.
// Anything unparseable is one item, because the button means "add this".
func ParseQuantity(s string) int32 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
	switch {
	case err != nil, n < 1:
		return 1
	case n > MaxLineQuantity:
		return MaxLineQuantity
	}
	return int32(n)
}

// ParseQuantityAllowingZero is the cart page's version, where zero means remove
// the line.
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

// Destination is where a shipping method delivers to, from
// shipping_methods.destination_kind.
type Destination string

const (
	// ToAddress wants a street address (home delivery).
	ToAddress Destination = "address"
	// ToPickupPoint wants a convenience-store pickup point.
	ToPickupPoint Destination = "pickup_point"
)

// DestinationFor turns the column's value into a Destination, refusing an
// unknown kind rather than defaulting to an address nobody can deliver to.
func DestinationFor(kind string) (Destination, bool) {
	switch Destination(kind) {
	case ToAddress:
		return ToAddress, true
	case ToPickupPoint:
		return ToPickupPoint, true
	}
	return "", false
}

// Address is the delivery detail a checkout collects, for either destination.
type Address struct {
	// To decides which half of this struct is real, and is set from the chosen
	// shipping method and never from the form.
	To Destination

	Email string
	Name  string
	Phone string

	PostalCode string
	City       string
	District   string
	Street     string

	PickupBrand     pickup.Brand
	PickupStoreCode string
	PickupStoreName string

	Note string
}

const (
	maxNameRunes       = 60
	maxPhoneRunes      = 30
	maxPostalCodeRunes = 6
	maxCityRunes       = 20
	maxDistrictRunes   = 20
	maxStreetRunes     = 200
	maxStoreNameRunes  = 40
	maxNoteRunes       = 500
	// Every checkout produces a carrier invoice. ECPay's Issue contract accepts
	// at most 80 bytes for CustomerEmail; accepting a longer delivery address and
	// truncating it later can turn a valid address into an invalid provider value.
	maxInvoiceEmailBytes = 80
	// The length ECPay publishes for a pickup-point store code, rather than any
	// one chain's own width.
	maxStoreCodeLen = 10
)

// Validate checks an address completely, and before anything is written.
func (a *Address) Validate() []account.FieldError {
	var errs []account.FieldError
	add := func(f string, k i18n.Key) { errs = append(errs, account.FieldError{Field: f, MessageKey: k}) }

	if k := emailError(a.Email); k != "" {
		add("email", k)
	}

	if strings.TrimSpace(a.Name) == "" {
		add("name", i18n.KeyNameRequired)
	} else if utf8.RuneCountInString(a.Name) > maxNameRunes {
		add("name", i18n.KeyNameTooLong)
	}

	switch {
	case strings.TrimSpace(a.Phone) == "":
		add("phone", i18n.KeyPhoneRequired)
	case !looksLikePhone(a.Phone):
		add("phone", i18n.KeyPhoneMalformed)
	}

	errs = append(errs, a.destinationErrors()...)

	if utf8.RuneCountInString(a.Note) > maxNoteRunes {
		add("note", i18n.KeyNoteTooLong)
	}

	return append(errs, a.controlCharErrors()...)
}

// destinationErrors validates the half of the struct that applies.
func (a *Address) destinationErrors() []account.FieldError {
	var errs []account.FieldError
	add := func(f string, k i18n.Key) { errs = append(errs, account.FieldError{Field: f, MessageKey: k}) }

	switch a.To {
	case ToAddress:
		if !isPostalCode(a.PostalCode) {
			add("postal_code", i18n.KeyPostalCodeMalformed)
		}
		switch {
		case strings.TrimSpace(a.City) == "":
			add("city", i18n.KeyCityRequired)
		case utf8.RuneCountInString(a.City) > maxCityRunes:
			add("city", i18n.KeyAddressIncomplete)
		}
		switch {
		case strings.TrimSpace(a.District) == "":
			add("district", i18n.KeyDistrictRequired)
		case utf8.RuneCountInString(a.District) > maxDistrictRunes:
			add("district", i18n.KeyAddressIncomplete)
		}
		switch {
		case strings.TrimSpace(a.Street) == "":
			add("street", i18n.KeyStreetRequired)
		case utf8.RuneCountInString(a.Street) > maxStreetRunes:
			add("street", i18n.KeyStreetTooLong)
		}
	case ToPickupPoint:
		if !a.PickupBrand.Known() {
			add("pickup_brand", i18n.KeyPickupBrandRequired)
		}
		if !isStoreCode(a.PickupStoreCode) {
			add("pickup_store_code", i18n.KeyStoreCodeMalformed)
		}
		switch {
		case strings.TrimSpace(a.PickupStoreName) == "":
			add("pickup_store_name", i18n.KeyStoreNameRequired)
		case utf8.RuneCountInString(a.PickupStoreName) > maxStoreNameRunes:
			add("pickup_store_name", i18n.KeyStoreNameTooLong)
		}
	default:
		add("shipping", i18n.KeyChooseShipping)
	}
	return errs
}

// ForDestination blanks the half of the address that does not apply, because
// order_private_data_one_destination refuses a row carrying both.
func (a *Address) ForDestination() {
	switch a.To {
	case ToAddress:
		a.PickupBrand, a.PickupStoreCode, a.PickupStoreName = "", "", ""
	case ToPickupPoint:
		a.PostalCode, a.City, a.District, a.Street = "", "", "", ""
	}
}

// isStoreCode reports whether s is a convenience-store number: digits or upper
// case, never digits alone — Hi-Life leads 149 of its 1,350 store codes with a
// letter (ECPay GetStoreList, 2026-08-06).
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

// controlCharErrors reports any field carrying a control character: a newline in
// a name is how a shipping label gets a line it was never given.
func (a *Address) controlCharErrors() []account.FieldError {
	var errs []account.FieldError
	for _, f := range []struct{ name, value string }{
		{"email", a.Email}, {"name", a.Name}, {"phone", a.Phone},
		{"postal_code", a.PostalCode}, {"city", a.City},
		{"district", a.District}, {"street", a.Street},
		{"pickup_brand", string(a.PickupBrand)}, {"pickup_store_code", a.PickupStoreCode},
		{"pickup_store_name", a.PickupStoreName}, {"note", a.Note},
	} {
		if hasControl(f.value) {
			errs = append(errs, account.FieldError{Field: f.name, MessageKey: i18n.KeyFieldHasControlChars})
		}
	}
	return errs
}

// emailError returns why an address is unusable, or "".
func emailError(s string) i18n.Key {
	switch {
	case strings.TrimSpace(s) == "":
		return i18n.KeyCheckoutEmailRequired
	case len(s) > maxInvoiceEmailBytes:
		return i18n.KeyCheckoutEmailTooLong
	case !email.Valid(s):
		return i18n.KeyCheckoutEmailMalformed
	}
	return ""
}

func looksLikePhone(s string) bool {
	if utf8.RuneCountInString(s) > maxPhoneRunes {
		return false
	}
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
	if len(s) < 3 || len(s) > maxPostalCodeRunes {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// hasControl reports whether s carries a control character. unicode.IsControl
// covers C1 (0x80–0x9F) as well as C0, which an ASCII-only check lets through.
func hasControl(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}

// Trim strips the whitespace around every field and uppercases the store code,
// which isStoreCode will not fold.
func (a *Address) Trim() {
	a.Email = strings.TrimSpace(a.Email)
	a.Name = strings.TrimSpace(a.Name)
	a.Phone = strings.TrimSpace(a.Phone)
	a.PostalCode = strings.TrimSpace(a.PostalCode)
	a.City = strings.TrimSpace(a.City)
	a.District = strings.TrimSpace(a.District)
	a.Street = strings.TrimSpace(a.Street)
	a.PickupBrand = pickup.Brand(strings.TrimSpace(string(a.PickupBrand)))
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

// Quote is what an order actually pays to be delivered. The surcharge is added
// AFTER the free-over threshold: the carrier still charges to cross the water.
type Quote struct {
	FeeCents  int64
	Surcharge int64
	ZoneName  string
}

// Total is what the order is charged for delivery. An error means the two
// non-negative database amounts cannot be represented safely as int64 cents.
func (q Quote) Total() (int64, error) {
	return addCheckoutMoney(q.FeeCents, q.Surcharge)
}

// HasSurcharge reports whether this address costs extra to reach.
func (q Quote) HasSurcharge() bool { return q.Surcharge > 0 }

// Invoice is what a customer wants on their uniform invoice.
type Invoice struct {
	// Type is the stable wire preference matching
	// invoice_preferences_type_known.
	Type invoicepkg.Preference
	// Carrier is the mobile-barcode invoice carrier, for mobile_carrier only.
	Carrier string
	// CompanyName is the registered buyer name corresponding to TaxID. It is
	// deliberately separate from the delivery recipient.
	CompanyName string
	// TaxID is the eight-digit business tax number, for company only.
	TaxID string
}

// Validate refuses what the schema would refuse, in the customer's language.
func (i *Invoice) Validate() []account.FieldError {
	i.Type = invoicepkg.Preference(strings.TrimSpace(string(i.Type)))
	i.Carrier = strings.ToUpper(strings.TrimSpace(i.Carrier))
	i.CompanyName = strings.TrimSpace(i.CompanyName)
	i.TaxID = strings.TrimSpace(i.TaxID)

	if i.Type == "" {
		i.Type = invoicepkg.PreferenceMember
	}
	if !i.Type.Known() {
		return []account.FieldError{{Field: "invoice_type", MessageKey: i18n.KeyInvoiceTypeRequired}}
	}

	var errs []account.FieldError
	switch i.Type {
	case invoicepkg.PreferenceMobile:
		if !invoicepkg.ValidMobileCarrier(i.Carrier) {
			errs = append(errs, account.FieldError{
				Field:      "invoice_carrier",
				MessageKey: i18n.KeyCarrierMalformed,
			})
		}
		i.CompanyName, i.TaxID = "", ""
	case invoicepkg.PreferenceCompany:
		if !invoicepkg.ValidBuyerName(i.CompanyName) {
			errs = append(errs, account.FieldError{
				Field:      "invoice_company_name",
				MessageKey: i18n.KeyCompanyNameMalformed,
			})
		}
		if !invoicepkg.ValidTaxID(i.TaxID) {
			errs = append(errs, account.FieldError{
				Field:      "invoice_tax_id",
				MessageKey: i18n.KeyTaxIDMalformed,
			})
		}
		i.Carrier = ""
	case invoicepkg.PreferenceMember:
		i.Carrier, i.CompanyName, i.TaxID = "", "", ""
	}
	return errs
}

// invoiceTypeLabelKey names the message for one choice.
func invoiceTypeLabelKey(t invoicepkg.Preference) i18n.Key {
	switch t {
	case invoicepkg.PreferenceMember:
		return i18n.KeyInvoiceMember
	case invoicepkg.PreferenceMobile:
		return i18n.KeyInvoiceMobile
	case invoicepkg.PreferenceCompany:
		return i18n.KeyInvoiceCompany
	default:
		panic("cart: no label for invoice type " + string(t))
	}
}
