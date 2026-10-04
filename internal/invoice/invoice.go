// Package invoice issues uniform invoices through ECPay's B2C e-invoice API.
// An unconfigured deployment issues nothing and says so; half a configuration
// does not start.
package invoice

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrDisabled = errors.New("invoice: no issuer configured")
	// ErrRejected is the provider refusing the document, carrying their own
	// reason.
	ErrRejected = errors.New("invoice: refused by the provider")
	// ErrAlreadyIssued is the caller-facing form of
	// invoice_documents_one_active_invoice_per_order.
	ErrAlreadyIssued = errors.New("invoice: order already has an invoice")
	ErrNotFound      = errors.New("invoice: not found")
	// ErrTooMuch means no authoritative whole-dollar refunded room remains for
	// another 折讓. It is a stable branch for a stale or concurrent form, not a
	// provider rejection; the amount itself is always derived by PostgreSQL.
	ErrTooMuch = errors.New("invoice: no refunded amount remains to relieve")
	// ErrClaimed is a 折讓 for this refund already filed or in flight. The way
	// out is ECPay's console, not a different figure.
	ErrClaimed = errors.New("invoice: already claimed")
	// ErrPending means the durable operation remains safe but has not converged:
	// another replica owns its lease, the provider result is not visible yet, or
	// an ambiguity needs the background reconciler/operator alarm.
	ErrPending = errors.New("invoice: reconciliation pending")
	// ErrAwaitingBuyer is an allowance ECPay has e-mailed the buyer to agree to;
	// it is opened only once they do.
	ErrAwaitingBuyer = errors.New("invoice: the allowance awaits the buyer's agreement")
	// ErrReason is a void claimed without a reason the 財政部 will accept.
	// claim_invoice_void raises invoice_void_reason; the handler must not 500.
	ErrReason = errors.New("invoice: void reason is required")
)

// TaxRate is Taiwan's business tax, 5%.
const TaxRate = 5

// MaxIssueItems is ECPay's maximum Items array length and therefore the
// highest ItemSeq it can represent. A sale reserves two positions for the
// canonical delivery and whole-dollar adjustment lines.
const (
	MaxIssueItems        = 999
	MaxIssueProductLines = MaxIssueItems - 2
)

// ValidTaxID applies the Ministry of Finance's current business-tax-number
// checksum. Since April 2023 the product sum is divisible by 5 (formerly 10).
// When the seventh digit is 7, its 7*4 contribution may be either 1 or 0.
//
// Source: https://www.fia.gov.tw/singlehtml/3?cntId=c4d9cff38c8642ef8872774ee9987283
func ValidTaxID(value string) bool {
	if len(value) != 8 {
		return false
	}
	if value == "00000000" {
		return false
	}
	weights := [...]int{1, 2, 1, 2, 1, 2, 4, 1}
	sum := 0
	seventhIsSeven := false
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
		digit := int(value[i] - '0')
		if i == 6 && digit == 7 {
			// The current MOF table represents 7*4 as a contribution of 1
			// or 0, rather than the ordinary decimal-digit sum 2+8.
			sum++
			seventhIsSeven = true
			continue
		}
		product := digit * weights[i]
		sum += product/10 + product%10
	}
	return sum%5 == 0 || (seventhIsSeven && (sum-1)%5 == 0)
}

var mobileBarcodePattern = regexp.MustCompile(`^/[0-9A-Z+\-.]{7}$`)

// ValidMobileBarcode reports whether value has the exact barcode shape ECPay
// accepts: a slash and seven upper-case letters, digits, +, - or dot.
func ValidMobileBarcode(value string) bool { return mobileBarcodePattern.MatchString(value) }

var donationCodePattern = regexp.MustCompile(`^\d{3,7}$`)

// ValidDonationCode is ECPay's LoveCode: three to seven digits, a leading zero
// allowed, so it is always handled as a string.
func ValidDonationCode(value string) bool { return donationCodePattern.MatchString(value) }

// ValidBuyerName applies the immutable filing/provider boundary shared by
// checkout and direct gateway callers. Control characters are never meaningful
// in an invoice buyer name and can change the shape of downstream records.
func ValidBuyerName(value string) bool {
	if strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > 60 {
		return false
	}
	return !strings.ContainsFunc(value, unicode.IsControl)
}

// Preference is the stable wire value shared by checkout, storage, back-office
// display, and the ECPay issuer. It is not a translated label.
type Preference string

const (
	PreferenceMember  Preference = "member_carrier"
	PreferenceMobile  Preference = "mobile_carrier"
	PreferenceCompany Preference = "company"
	PreferenceDonate  Preference = "donation"
)

var offeredPreferences = [...]Preference{
	PreferenceMember,
	PreferenceMobile,
	PreferenceCompany,
	PreferenceDonate,
}

// OfferedPreferences returns a copy, so callers cannot mutate the canonical closed set.
func OfferedPreferences() []Preference { return slices.Clone(offeredPreferences[:]) }

func (p Preference) Known() bool {
	for _, offered := range offeredPreferences {
		if p == offered {
			return true
		}
	}
	return false
}

func (p Preference) NeedsMobileBarcode() bool { return p == PreferenceMobile }

func (p Preference) NeedsTaxID() bool { return p == PreferenceCompany }

// InvoiceCarrier is the 載具 an invoice is filed under, in ECPay's CarrierType
// spelling. It is never the parcel company.
type InvoiceCarrier string

const (
	// InvoiceCarrierNone is a printed invoice or one held in the shop's own account.
	InvoiceCarrierNone          InvoiceCarrier = ""
	InvoiceCarrierMember        InvoiceCarrier = "1"
	InvoiceCarrierMobileBarcode InvoiceCarrier = "3"
)

// DocumentKind is invoice_documents.kind, closed by invoice_documents_kind_known.
type DocumentKind string

// DocumentStatus is invoice_documents.status, closed by invoice_documents_status_known.
type DocumentStatus string

const (
	DocumentInvoice   DocumentKind = "invoice"
	DocumentAllowance DocumentKind = "allowance"

	DocumentIssued DocumentStatus = "issued"
	DocumentVoided DocumentStatus = "voided"
)

type Document struct {
	ID     string
	Kind   DocumentKind
	Number string
	// AmountCents is what the document is for, tax included.
	AmountCents int64
	Status      DocumentStatus
	IssuedAt    time.Time
	// ProviderRef is ECPay's own handle on it. For an invoice that is the
	// RandomNumber, which is what a void needs alongside the number.
	ProviderRef string
	Lines       []Line
}

func (d Document) Voided() bool { return d.Status == DocumentVoided }

type Line struct {
	Description string `json:"description"`
	Quantity    int32  `json:"quantity"`
	// UnitPriceCents and AmountCents are tax-INCLUSIVE, matching what the
	// customer was charged.
	UnitPriceCents int64 `json:"unit_price_cents"`
	AmountCents    int64 `json:"amount_cents"`
}
