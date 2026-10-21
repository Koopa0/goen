// Package checkout is turning a shopper's cart into a placed order; it holds
// the checkout page with its store-pickup round trip, placement, the quote,
// attempts, drafts, the invoice preference, and coupon and credit spending; it
// does not hold the cart itself (cart), the placed order afterwards (order,
// order/buyer) or taking the money (payment).
package checkout

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/coupon"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/web"
)

var (
	// ErrCreditChanged means available store credit moved during checkout; the
	// form must show the fresh figure before a second submission.
	ErrCreditChanged = errors.New("checkout: available store credit changed")
	// ErrBusy is a write the database ended early, for checkout a lock wait
	// that outlived statement_timeout. Nothing the customer typed is wrong, so
	// it is a 422 asking them to submit again.
	ErrBusy          = errors.New("checkout: the database ended the statement before it finished")
	ErrEmpty         = errors.New("checkout: empty")
	ErrMixedTaxTypes = errors.New("checkout: taxable and exempt items need separate orders")
	// errCheckoutChanged means the facts no longer match the quote the customer
	// confirmed; show the refreshed quote before retrying.
	errCheckoutChanged = errors.New("checkout: checkout quote changed")
	// errCheckoutKeyConflict stays private: Handler replaces it rather than
	// expose whether a guessed key exists.
	errCheckoutKeyConflict = errors.New("checkout: checkout key belongs to another cart")
	// errCheckoutMoney means server-owned state cannot be represented as the
	// int64 cents an order stores; browser input never supplies money.
	errCheckoutMoney = errors.New("checkout: checkout money is out of range")
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
		return checkoutAttemptID{}, errors.New("checkout: malformed checkout attempt ID")
	}
	var id checkoutAttemptID
	copy(id[:], decoded)
	if id == (checkoutAttemptID{}) || id.String() != value {
		return checkoutAttemptID{}, errors.New("checkout: malformed checkout attempt ID")
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
		return nil, "", errors.New("checkout: checkout quote contains too many lines")
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
		return "", errors.New("checkout: incomplete checkout quote")
	}
	if q.ShippingCents < 0 || q.DiscountCents < 0 || q.CreditCents < 0 {
		return "", errors.New("checkout: checkout quote contains negative money")
	}

	code = coupon.Normalise(q.CouponCode)
	if code != q.CouponCode || (code != "" && !coupon.ValidCode(code)) {
		return "", errors.New("checkout: checkout quote contains a non-canonical coupon code")
	}
	if code == "" && q.DiscountCents != 0 {
		return "", errors.New("checkout: checkout quote discounts without a coupon")
	}
	return code, nil
}

func (q checkoutQuote) validateIdentityTotal(subtotal int64) error {
	gross, err := checkoutGross(subtotal, q.ShippingCents, q.DiscountCents)
	if err != nil {
		return fmt.Errorf("checkout: checkout quote total is invalid: %w", err)
	}
	if q.CreditCents > gross {
		return errors.New("checkout: checkout quote credit exceeds its total")
	}
	return nil
}

func checkoutQuoteSubtotal(lines []checkoutQuoteLine) (int64, error) {
	var subtotal int64
	for i := range lines {
		line := lines[i]
		if line.VariantID == uuid.Nil || line.Quantity <= 0 || line.UnitCents < 0 {
			return 0, errors.New("checkout: checkout quote contains an invalid line")
		}
		if i > 0 && lines[i-1].VariantID == line.VariantID {
			return 0, errors.New("checkout: checkout quote contains a duplicate variant")
		}
		var err error
		subtotal, err = addCheckoutLine(subtotal, line.UnitCents, line.Quantity)
		if err != nil {
			return 0, fmt.Errorf("checkout: checkout quote subtotal: %w", err)
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
		return checkoutQuoteID{}, errors.New("checkout: malformed checkout quote ID")
	}
	var id checkoutQuoteID
	copy(id[:], decoded)
	if id == (checkoutQuoteID{}) || id.String() != value {
		return checkoutQuoteID{}, errors.New("checkout: malformed checkout quote ID")
	}
	return id, nil
}

// ShippingQuote adds the surcharge AFTER the free-over threshold: the carrier
// still charges to cross the water.
type ShippingQuote struct {
	FeeCents  int64
	Surcharge int64
	ZoneName  string
}

// Total fails when the two database amounts cannot be represented as int64
// cents.
func (q ShippingQuote) Total() (int64, error) {
	return addCheckoutMoney(q.FeeCents, q.Surcharge)
}

type Invoice struct {
	Type          invoice.Preference
	MobileBarcode string
	DonationCode  string
	// CompanyName is the buyer name for TaxID, kept apart from the delivery
	// recipient.
	CompanyName string
	TaxID       string
}

func (i *Invoice) Validate() []web.FieldRefusal {
	i.Type = invoice.Preference(strings.TrimSpace(string(i.Type)))
	i.MobileBarcode = strings.ToUpper(strings.TrimSpace(web.FoldWidth(i.MobileBarcode)))
	i.DonationCode = strings.TrimSpace(i.DonationCode)
	i.CompanyName = strings.TrimSpace(i.CompanyName)
	i.TaxID = strings.TrimSpace(web.FoldWidth(i.TaxID))

	if i.Type == "" {
		i.Type = invoice.PreferenceMember
	}
	if !i.Type.Known() {
		return []web.FieldRefusal{{Field: "invoice_type", MessageKey: i18n.KeyInvoiceTypeRequired}}
	}

	var errs []web.FieldRefusal
	if i.Type != invoice.PreferenceDonate {
		i.DonationCode = ""
	}
	switch i.Type {
	case invoice.PreferenceMobile:
		if !invoice.ValidMobileBarcode(i.MobileBarcode) {
			errs = append(errs, web.FieldRefusal{
				Field:      "invoice_carrier",
				MessageKey: i18n.KeyMobileBarcodeMalformed,
			})
		}
		i.CompanyName, i.TaxID = "", ""
	case invoice.PreferenceDonate:
		if !invoice.ValidDonationCode(i.DonationCode) {
			errs = append(errs, web.FieldRefusal{Field: "invoice_donation_code", MessageKey: i18n.KeyDonationCodeMalformed})
		}
		i.MobileBarcode, i.CompanyName, i.TaxID = "", "", ""
	case invoice.PreferenceCompany:
		if !invoice.ValidBuyerName(i.CompanyName) {
			errs = append(errs, web.FieldRefusal{
				Field:      "invoice_company_name",
				MessageKey: i18n.KeyCompanyNameMalformed,
			})
		}
		if !invoice.ValidTaxID(i.TaxID) {
			errs = append(errs, web.FieldRefusal{
				Field:      "invoice_tax_id",
				MessageKey: i18n.KeyTaxIDMalformed,
			})
		}
		i.MobileBarcode = ""
	case invoice.PreferenceMember:
		i.MobileBarcode, i.CompanyName, i.TaxID = "", "", ""
	}
	return errs
}

func invoiceTypeLabelKey(t invoice.Preference) i18n.Key {
	switch t {
	case invoice.PreferenceMember:
		return i18n.KeyInvoiceMember
	case invoice.PreferenceMobile:
		return i18n.KeyInvoiceMobile
	case invoice.PreferenceDonate:
		return i18n.KeyInvoiceDonate
	case invoice.PreferenceCompany:
		return i18n.KeyInvoiceCompany
	default:
		panic("checkout: no label for invoice type " + string(t))
	}
}
