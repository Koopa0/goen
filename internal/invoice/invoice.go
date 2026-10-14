// Package invoice is the vocabulary of Taiwanese uniform invoices: the
// buyer's preference, the document and its lines, tax types and units, the
// identifier checks and the attention categories. It imports nothing but the
// standard library and i18n, so a page can name an invoice without reaching
// the database, the outbox or the mail sender. Issuing them is
// invoice/issuer's work.
package invoice

import (
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
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

type Line struct {
	TaxType     TaxType  `json:"tax_type"`
	Unit        ItemUnit `json:"unit"`
	Description string   `json:"description"`
	Quantity    int32    `json:"quantity"`
	// UnitPriceCents and AmountCents are tax-INCLUSIVE, matching what the
	// customer was charged.
	UnitPriceCents int64 `json:"unit_price_cents"`
	AmountCents    int64 `json:"amount_cents"`
}
