// Package cart holds goen's cart and checkout. A cart is identified by a cookie
// whose random token the database stores only as a SHA-256 digest.
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

var (
	// ErrNotFound is a cart, order or variant that does not exist.
	ErrNotFound = errors.New("cart: not found")
	// ErrUnavailable is a variant that cannot be added or ordered.
	ErrUnavailable = errors.New("cart: variant unavailable")
	// ErrEmpty is a checkout with nothing in the cart.
	ErrEmpty = errors.New("cart: empty")
)

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

// PayWindow is how long after placing an order a customer may still start paying.
const PayWindow = 30 * time.Minute

// StripeSessionFloor mirrors payment.MinSessionLifetime, which is Stripe's own
// minimum for a Checkout Session's expires_at.
const StripeSessionFloor = 30 * time.Minute

// HoldTTL is how long an order's stock is reserved while payment is attempted.
// A sum, because with HoldTTL equal to the floor no session can be opened at all.
const HoldTTL = PayWindow + StripeSessionFloor

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

	PickupBrand     string
	PickupStoreCode string
	PickupStoreName string

	Note string
}

// FieldError names one rejected field and the message key saying why.
type FieldError struct {
	Field      string
	MessageKey i18n.Key
}

const (
	maxNameRunes      = 60
	maxStreetRunes    = 200
	maxStoreNameRunes = 40
	maxNoteRunes      = 500
	maxEmailRunes     = 254
	// The length ECPay publishes for a pickup-point store code, rather than any
	// one chain's own width.
	maxStoreCodeLen = 10
)

// Validate checks an address completely, and before anything is written.
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

// hasControl reports whether s carries a control character. unicode.IsControl
// covers C1 (0x80–0x9F) as well as C0, which an ASCII-only check lets through.
func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
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

// Quote is what an order actually pays to be delivered. The surcharge is added
// AFTER the free-over threshold: the carrier still charges to cross the water.
type Quote struct {
	FeeCents  int64
	Surcharge int64
	ZoneName  string
}

// Total is what the order is charged for delivery.
func (q Quote) Total() int64 { return q.FeeCents + q.Surcharge }

// HasSurcharge reports whether this address costs extra to reach.
func (q Quote) HasSurcharge() bool { return q.Surcharge > 0 }

// Invoice is what a customer wants on their uniform invoice.
type Invoice struct {
	// Type is 'mobile_carrier', 'member_carrier' or 'company', matching
	// invoice_preferences_type_known.
	Type string
	// Carrier is the mobile-barcode invoice carrier, for mobile_carrier only.
	Carrier string
	// TaxID is the eight-digit business tax number, for company only.
	TaxID string
}

// InvoiceTypes is every choice the form offers, in order, and the same list as
// invoice_preferences_type_known.
var InvoiceTypes = []string{"member_carrier", "mobile_carrier", "company"}

// mobileCarrier is the barcode format the Ministry of Finance issues: a slash
// followed by seven characters drawn from digits, capitals, and + - . only.
var mobileCarrier = regexp.MustCompile(`^/[0-9A-Z+\-.]{7}$`)

// taxID is the eight-digit business tax number.
//
//nolint:gocritic // regexpSimplify: kept character-for-character identical to
var taxID = regexp.MustCompile(`^[0-9]{8}$`)

// Validate refuses what the schema would refuse, in the customer's language.
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
