// Package cart holds goen's cart and checkout. A cart is identified by a cookie
// whose random token the database stores only as a SHA-256 digest.
package cart

import (
	"bytes"
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

	"github.com/koopa0/goen/internal/destination"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	invoicepkg "github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/web"
)

var (
	ErrNotFound = errors.New("cart: not found")
	// ErrNotYourCart is a token naming another account's cart, or any account's
	// when nobody is signed in.
	ErrNotYourCart = errors.New("cart: the cart belongs to an account the requester is not signed in as")
	ErrUnavailable = errors.New("cart: variant unavailable")
	// ErrCreditChanged means available store credit moved during checkout; the
	// form must show the fresh figure before a second submission.
	ErrCreditChanged = errors.New("cart: available store credit changed")
	// ErrBusy is a write the database ended early, for checkout a lock wait
	// that outlived statement_timeout. Nothing the customer typed is wrong, so
	// it is a 422 asking them to submit again.
	ErrBusy  = errors.New("cart: the database ended the statement before it finished")
	ErrEmpty = errors.New("cart: empty")
	// ErrTooManyItems means no room for another distinct product within ECPay's
	// Items limit per invoice.
	ErrTooManyItems  = errors.New("cart: too many invoice items")
	ErrMixedTaxTypes = errors.New("cart: taxable and exempt items need separate orders")
	// ErrQuantityAdjusted means the write succeeded but kept less than
	// requested, because stock fell short.
	ErrQuantityAdjusted = errors.New("cart: quantity adjusted to available stock")
	// errCheckoutChanged means the facts no longer match the quote the customer
	// confirmed; show the refreshed quote before retrying.
	errCheckoutChanged = errors.New("cart: checkout quote changed")
	// errCheckoutKeyConflict stays private: Handler replaces it rather than
	// expose whether a guessed key exists.
	errCheckoutKeyConflict = errors.New("cart: checkout key belongs to another cart")
	// errCheckoutMoney means server-owned state cannot be represented as the
	// int64 cents an order stores; browser input never supplies money.
	errCheckoutMoney = errors.New("cart: checkout money is out of range")
)

type checkoutQuoteLine struct {
	VariantID uuid.UUID
	Quantity  int32
	UnitCents int64
}

// checkoutQuote is not persisted and its ID is no credential: checkout rebuilds
// the facts from locked rows and accepts only equality.
type checkoutQuote struct {
	CartID uuid.UUID
	Lines  []checkoutQuoteLine

	ShippingVersionID uuid.UUID
	ShippingCents     int64

	CouponCode    string
	DiscountCents int64
	CreditCents   int64
}

// checkoutQuoteID is an array so arbitrary strings cannot reach
// Store.placeOrder.
type checkoutQuoteID [sha256.Size]byte

const checkoutAttemptIDBytes = 16

// checkoutAttemptID is an array so arbitrary form text stays out of the store;
// only the HTTP boundary and test facades parse its wire form.
type checkoutAttemptID [checkoutAttemptIDBytes]byte

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

func (id checkoutAttemptID) String() string {
	return base64.RawURLEncoding.EncodeToString(id[:])
}

// ID copies the lines before sorting, so it never mutates the caller's state.
func (q checkoutQuote) ID() (checkoutQuoteID, error) {
	lines, code, err := q.identityFacts()
	if err != nil {
		return checkoutQuoteID{}, err
	}

	encoded := make([]byte, 0, 128+len(lines)*28+len(code))
	encoded = append(encoded, "goen-checkout-quote\x00v1\x00"...)
	encoded = append(encoded, q.CartID[:]...)
	encoded = binary.BigEndian.AppendUint32(encoded, uint32(len(lines))) //nolint:gosec // proven bounded
	for i := range lines {
		line := &lines[i]
		encoded = append(encoded, line.VariantID[:]...)
		encoded = binary.BigEndian.AppendUint32(encoded, uint32(line.Quantity))  //nolint:gosec // proven non-negative
		encoded = binary.BigEndian.AppendUint64(encoded, uint64(line.UnitCents)) //nolint:gosec // proven non-negative
	}
	encoded = append(encoded, q.ShippingVersionID[:]...)
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(q.ShippingCents)) //nolint:gosec // proven non-negative
	encoded = binary.BigEndian.AppendUint32(encoded, uint32(len(code)))       //nolint:gosec // regex-bounded to 32
	encoded = append(encoded, code...)
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(q.DiscountCents)) //nolint:gosec // proven non-negative
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(q.CreditCents))   //nolint:gosec // proven non-negative
	return sha256.Sum256(encoded), nil
}

// identityFacts validates the domain constraints that make each unsigned wire
// conversion in ID safe.
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

// addCheckoutLine and addCheckoutMoney return an error rather than wrap on
// overflow.
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

func addCheckoutMoney(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, errCheckoutMoney
	}
	return a + b, nil
}

// checkoutGross is the one definition of subtotal + shipping - discount, shared
// by the quote identity and locked placement.
func checkoutGross(subtotal, shipping, discount int64) (int64, error) {
	beforeDiscount, err := addCheckoutMoney(subtotal, shipping)
	if err != nil || discount < 0 || discount > beforeDiscount {
		return 0, errCheckoutMoney
	}
	return beforeDiscount - discount, nil
}

func (id checkoutQuoteID) String() string {
	return base64.RawURLEncoding.EncodeToString(id[:])
}

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

// CookieName carries the __Host- prefix, which binds it to this origin, so a
// sibling subdomain cannot write a cart cookie goen would trust.
const CookieName = "__Host-goen_cart"

const cookieMaxAge = 30 * 24 * 60 * 60 // 30 days

const tokenBytes = 32

// MaxLineQuantity matches the schema's CHECK.
const MaxLineQuantity = 999

// payWindow leaves the public 60-minute stock hold room for Stripe's 30-minute
// floor plus the creation margin.
const payWindow = 29 * time.Minute

// stripeSessionFloor is Stripe's minimum for a Checkout Session's expires_at.
const stripeSessionFloor = 30 * time.Minute

// stripeSessionStartMargin absorbs Unix second truncation, clock skew and the
// trip to Stripe; without it the end of payWindow is already too late to create
// a session.
const stripeSessionStartMargin = time.Minute

// holdTTL: the floor and margin are inventory time, not extra pay time; they
// let a session started at payWindow expire with the same hold.
const holdTTL = payWindow + stripeSessionFloor + stripeSessionStartMargin

func NewToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// SetCookie writes the cart cookie; a __Host- cookie without Secure is rejected by the browser, so
// development uses a different name.
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

// A browser replaces a cookie only with one of the same name and path, and
// refuses a __Host- name without Secure; expireCookie sends both set-time
// attributes.
func expireCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: dev-only opt-out, secure by default
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

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

// ParseQuantity clamps to what a line may hold; unparseable is one item,
// because the button means "add this".
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

// ParseQuantityAllowingZero is the cart page's version, where zero removes the
// line.
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

type Address struct {
	// To picks which half is real; it comes from the shipping method, never the
	// form.
	To destination.Kind

	Email string
	Name  string
	Phone string

	PostalCode string
	City       string
	District   string
	Street     string

	PickupChain     pickup.Chain
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
	// Every checkout produces an invoice preference. ECPay's Issue contract accepts
	// at most 80 bytes for CustomerEmail; accepting a longer delivery address and
	// truncating it later can turn a valid address into an invalid provider value.
	maxInvoiceEmailBytes = 80
	// ECPay's published pickup-point store code length, not any one chain's
	// width.
	maxStoreCodeLen = 10
)

func (a *Address) Validate() []web.FieldRefusal {
	var errs []web.FieldRefusal
	add := func(f string, k i18n.Key) { errs = append(errs, web.FieldRefusal{Field: f, MessageKey: k}) }

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

func (a *Address) destinationErrors() []web.FieldRefusal {
	var errs []web.FieldRefusal
	add := func(f string, k i18n.Key) { errs = append(errs, web.FieldRefusal{Field: f, MessageKey: k}) }

	switch a.To {
	case destination.Address:
		switch {
		case strings.TrimSpace(a.PostalCode) == "":
			add("postal_code", i18n.KeyPostalCodeRequired)
		case !isPostalCode(a.PostalCode):
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
	case destination.PickupPoint:
		errs = append(errs, a.pickupPointErrors()...)
	default:
		add("shipping", i18n.KeyChooseShipping)
	}
	return errs
}

// pickupPointErrors asks a shopper only for the chain. A store number and name
// come from the back office and are checked only when present, since the
// carrier's picker supplies them; once either is written,
// order_private_data_pickup_complete refuses a row without the other.
func (a *Address) pickupPointErrors() []web.FieldRefusal {
	var errs []web.FieldRefusal
	add := func(f string, k i18n.Key) { errs = append(errs, web.FieldRefusal{Field: f, MessageKey: k}) }

	if !a.PickupChain.Known() {
		add("pickup_chain", i18n.KeyPickupChainRequired)
	}
	switch {
	case a.PickupStoreCode == "" && a.PickupStoreName != "":
		// "" satisfies neither half of the 1-to-10 digits-or-letters shape, so a
		// name with no code names the code field as what needs fixing.
		add("pickup_store_code", i18n.KeyStoreCodeMalformed)
	case a.PickupStoreCode != "" && a.PickupStoreName == "":
		add("pickup_store_name", i18n.KeyAddressIncomplete)
	case a.PickupStoreCode != "" && !isStoreCode(a.PickupStoreCode):
		add("pickup_store_code", i18n.KeyStoreCodeMalformed)
	}
	if utf8.RuneCountInString(a.PickupStoreName) > maxStoreNameRunes {
		add("pickup_store_name", i18n.KeyStoreNameTooLong)
	}
	return errs
}

// DropOtherDestination blanks the half of the address that does not apply, because
// order_private_data_one_destination refuses a row carrying both.
func (a *Address) DropOtherDestination() {
	switch a.To {
	case destination.Address:
		a.PickupChain, a.PickupStoreCode, a.PickupStoreName = "", "", ""
	case destination.PickupPoint:
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

// controlCharErrors: a newline in a name is how a shipping label gets a line it
// was never given.
func (a *Address) controlCharErrors() []web.FieldRefusal {
	var errs []web.FieldRefusal
	for _, f := range []struct{ name, value string }{
		{"email", a.Email}, {"name", a.Name}, {"phone", a.Phone},
		{"postal_code", a.PostalCode}, {"city", a.City},
		{"district", a.District}, {"street", a.Street},
		{"pickup_chain", string(a.PickupChain)}, {"pickup_store_code", a.PickupStoreCode},
		{"pickup_store_name", a.PickupStoreName}, {"note", a.Note},
	} {
		if hasControl(f.value) {
			errs = append(errs, web.FieldRefusal{Field: f.name, MessageKey: i18n.KeyFieldHasControlChars})
		}
	}
	return errs
}

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

// Trim folds full-width digits in the phone and postal code and uppercases the
// store code, which isStoreCode will not fold.
func (a *Address) Trim() {
	a.Email = strings.TrimSpace(a.Email)
	a.Name = strings.TrimSpace(a.Name)
	a.Phone = strings.TrimSpace(web.FoldWidth(a.Phone))
	a.PostalCode = strings.TrimSpace(web.FoldWidth(a.PostalCode))
	a.City = strings.TrimSpace(a.City)
	a.District = strings.TrimSpace(a.District)
	a.Street = strings.TrimSpace(a.Street)
	a.PickupChain = pickup.Chain(strings.TrimSpace(string(a.PickupChain)))
	a.PickupStoreCode = strings.ToUpper(strings.TrimSpace(a.PickupStoreCode))
	a.PickupStoreName = strings.TrimSpace(a.PickupStoreName)
	a.Note = strings.TrimSpace(a.Note)
}

// ShippingFee treats a free-over threshold of zero as never free.
func ShippingFee(feeCents, freeOverCents, subtotalCents int64) int64 {
	if freeOverCents > 0 && subtotalCents >= freeOverCents {
		return 0
	}
	return feeCents
}

// Quote adds the surcharge AFTER the free-over threshold: the carrier still
// charges to cross the water.
type Quote struct {
	FeeCents  int64
	Surcharge int64
	ZoneName  string
}

// Total fails when the two database amounts cannot be represented as int64
// cents.
func (q Quote) Total() (int64, error) {
	return addCheckoutMoney(q.FeeCents, q.Surcharge)
}

func (q Quote) HasSurcharge() bool { return q.Surcharge > 0 }

type Invoice struct {
	Type          invoicepkg.Preference
	MobileBarcode string
	DonationCode  string
	// CompanyName is the buyer name for TaxID, kept apart from the delivery
	// recipient.
	CompanyName string
	TaxID       string
}

func (i *Invoice) Validate() []web.FieldRefusal {
	i.Type = invoicepkg.Preference(strings.TrimSpace(string(i.Type)))
	i.MobileBarcode = strings.ToUpper(strings.TrimSpace(web.FoldWidth(i.MobileBarcode)))
	i.DonationCode = strings.TrimSpace(i.DonationCode)
	i.CompanyName = strings.TrimSpace(i.CompanyName)
	i.TaxID = strings.TrimSpace(web.FoldWidth(i.TaxID))

	if i.Type == "" {
		i.Type = invoicepkg.PreferenceMember
	}
	if !i.Type.Known() {
		return []web.FieldRefusal{{Field: "invoice_type", MessageKey: i18n.KeyInvoiceTypeRequired}}
	}

	var errs []web.FieldRefusal
	if i.Type != invoicepkg.PreferenceDonate {
		i.DonationCode = ""
	}
	switch i.Type {
	case invoicepkg.PreferenceMobile:
		if !invoicepkg.ValidMobileBarcode(i.MobileBarcode) {
			errs = append(errs, web.FieldRefusal{
				Field:      "invoice_carrier",
				MessageKey: i18n.KeyMobileBarcodeMalformed,
			})
		}
		i.CompanyName, i.TaxID = "", ""
	case invoicepkg.PreferenceDonate:
		if !invoicepkg.ValidDonationCode(i.DonationCode) {
			errs = append(errs, web.FieldRefusal{Field: "invoice_donation_code", MessageKey: i18n.KeyDonationCodeMalformed})
		}
		i.MobileBarcode, i.CompanyName, i.TaxID = "", "", ""
	case invoicepkg.PreferenceCompany:
		if !invoicepkg.ValidBuyerName(i.CompanyName) {
			errs = append(errs, web.FieldRefusal{
				Field:      "invoice_company_name",
				MessageKey: i18n.KeyCompanyNameMalformed,
			})
		}
		if !invoicepkg.ValidTaxID(i.TaxID) {
			errs = append(errs, web.FieldRefusal{
				Field:      "invoice_tax_id",
				MessageKey: i18n.KeyTaxIDMalformed,
			})
		}
		i.MobileBarcode = ""
	case invoicepkg.PreferenceMember:
		i.MobileBarcode, i.CompanyName, i.TaxID = "", "", ""
	}
	return errs
}

func invoiceTypeLabelKey(t invoicepkg.Preference) i18n.Key {
	switch t {
	case invoicepkg.PreferenceMember:
		return i18n.KeyInvoiceMember
	case invoicepkg.PreferenceMobile:
		return i18n.KeyInvoiceMobile
	case invoicepkg.PreferenceDonate:
		return i18n.KeyInvoiceDonate
	case invoicepkg.PreferenceCompany:
		return i18n.KeyInvoiceCompany
	default:
		panic("cart: no label for invoice type " + string(t))
	}
}
