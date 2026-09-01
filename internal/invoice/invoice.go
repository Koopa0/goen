// Package invoice issues uniform invoices through ECPay's B2C e-invoice API.
//
// It runs against ECPay's published staging credentials, so going live is a
// credential change rather than a rewrite. An unconfigured deployment issues
// nothing and says so; half a configuration does not start.
package invoice

import (
	"errors"
	"slices"
	"time"
)

// Errors a caller branches on.
var (
	// ErrDisabled is an issuer with no credentials; nothing was filed.
	ErrDisabled = errors.New("invoice: no issuer configured")
	// ErrRejected is the provider refusing the document, carrying their own
	// reason because it names what to fix.
	ErrRejected = errors.New("invoice: refused by the provider")
	// ErrAlreadyIssued is the caller-facing form of
	// invoice_documents_one_active_invoice_per_order.
	ErrAlreadyIssued = errors.New("invoice: order already has an invoice")
	// ErrNotFound is an order or a document that does not exist.
	ErrNotFound = errors.New("invoice: not found")
	// ErrTooMuch is a 折讓 relieving more than has gone back to the customer.
	// Its own sentinel and not a shade of ErrRejected, because the two send a
	// staff member to different places: this one to the figure they typed, and
	// ErrRejected to the 統編 or carrier the provider named. Telling them apart
	// by searching the error TEXT is mistake #32.
	ErrTooMuch = errors.New("invoice: more than was refunded")
	// ErrClaimed is a 折讓 for this refund already filed or in flight. The way
	// out is ECPay's console, not a different figure.
	ErrClaimed = errors.New("invoice: already claimed")
)

// TaxRate is Taiwan's business tax, 5%.
const TaxRate = 5

// Preference is the stable wire value shared by checkout, storage, back-office
// display, and the ECPay issuer. It is not a translated label.
type Preference string

const (
	PreferenceMember  Preference = "member_carrier"
	PreferenceMobile  Preference = "mobile_carrier"
	PreferenceCompany Preference = "company"
)

var offeredPreferences = [...]Preference{
	PreferenceMember,
	PreferenceMobile,
	PreferenceCompany,
}

// OfferedPreferences returns the checkout choices in display order. The result
// owns its storage, so callers cannot mutate the canonical closed set.
func OfferedPreferences() []Preference { return slices.Clone(offeredPreferences[:]) }

// Known reports whether p is one of the preferences this shop can issue.
func (p Preference) Known() bool {
	for _, offered := range offeredPreferences {
		if p == offered {
			return true
		}
	}
	return false
}

// NeedsCarrier reports whether checkout must collect a mobile barcode.
func (p Preference) NeedsCarrier() bool { return p == PreferenceMobile }

// NeedsTaxID reports whether checkout must collect a business tax number.
func (p Preference) NeedsTaxID() bool { return p == PreferenceCompany }

// Carrier types, as ECPay names them.
const (
	// CarrierNone is a printed invoice or one held in the shop's own account.
	CarrierNone = ""
	// CarrierMember is ECPay's own member carrier.
	CarrierMember = "1"
	// CarrierMobile is the mobile barcode carrier a customer carries.
	CarrierMobile = "3"
)

// Document is an issued uniform invoice or credit note, as goen records it.
type Document struct {
	ID     string
	Kind   string
	Number string
	// AmountCents is what the document is for, tax included.
	AmountCents int64
	Status      string
	IssuedAt    time.Time
	// ProviderRef is ECPay's own handle on it. For an invoice that is the
	// RandomNumber, which is what a void needs alongside the number.
	ProviderRef string
	Lines       []Line
}

// Voided reports whether this document has been cancelled.
func (d Document) Voided() bool { return d.Status == "voided" }

// Line is one item on an invoice.
type Line struct {
	Description string
	Quantity    int32
	// UnitPriceCents and AmountCents are tax-INCLUSIVE, matching what the
	// customer was charged.
	UnitPriceCents int64
	AmountCents    int64
}
