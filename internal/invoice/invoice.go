// Package invoice issues 統一發票 through 綠界 (ECPay)'s B2C e-invoice API.
//
// # Why a real integration against a test environment
//
// Issuing a 統一發票 needs a 加值中心, and a real one needs a business
// registration and a contract. ECPay publishes a staging environment with
// credentials anybody may use (MerchantID 2000132), so the code here is the real
// thing — real HTTP, real AES envelope, real error codes, real void and
// allowance — and going live is a credential change rather than a rewrite.
//
// The alternative was a fake issuer that emits invoice-shaped numbers, and this
// repository has refused that from the start: something that LOOKS like a tax
// document and is not is worse than honest absence, because only the second is
// visible.
//
// # The off-switch
//
// An unconfigured deployment issues nothing and says so, exactly as an empty
// GOEN_STRIPE_SECRET_KEY leaves the payment page saying 金流尚未啟用. Half-on is
// the state neither has: a merchant id without its keys does not start.
package invoice

import (
	"errors"
	"time"
)

// Errors a caller branches on.
var (
	// ErrDisabled is an issuer with no credentials. The order stands and the
	// preference is recorded; nothing was filed with the 加值中心.
	ErrDisabled = errors.New("invoice: no issuer configured")
	// ErrRejected is the provider refusing the document — a malformed 統編, a
	// carrier that does not exist, an amount that does not add up. The message
	// carries the provider's own reason, because it names what to fix.
	ErrRejected = errors.New("invoice: refused by the provider")
	// ErrAlreadyIssued is an order that already has a live invoice.
	// invoice_documents_one_active_invoice_per_order refuses the second; this is
	// the caller-facing form.
	ErrAlreadyIssued = errors.New("invoice: order already has an invoice")
	// ErrNotFound is an order or a document that does not exist.
	ErrNotFound = errors.New("invoice: not found")
)

// TaxRate is 營業稅, 5% in Taiwan.
//
// SalesAmount in an ECPay B2C request is the TAX-INCLUSIVE total, which is what
// a Taiwanese consumer price already is: a shelf price of NT$590 is NT$562 plus
// NT$28 of tax, not NT$590 plus tax. So goen sends its own totals unchanged and
// this constant exists for the ITEMISATION only — vat='1' tells ECPay the item
// prices include tax and it derives the split itself.
const TaxRate = 5

// Carrier types, as ECPay names them.
const (
	// CarrierNone is a printed invoice or one held in the shop's own account.
	// goen never prints, so this is the 會員載具 case: ECPay holds it against
	// the customer's email.
	CarrierNone = ""
	// CarrierMember is ECPay's own member carrier.
	CarrierMember = "1"
	// CarrierMobile is 手機條碼載具, the /XXXXXXX barcode a customer carries.
	CarrierMobile = "3"
)

// Document is an issued 統一發票 or 折讓, as goen records it.
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
	// Lines is what the document says was sold. The itemisation is why
	// invoice_document_lines exists rather than a single amount: a shop
	// reconciling an invoice against an order compares lines, not totals.
	Lines []Line
}

// Voided reports whether this document has been cancelled.
func (d Document) Voided() bool { return d.Status == "voided" }

// Line is one item on an invoice.
type Line struct {
	Description string
	Quantity    int32
	// UnitPriceCents and AmountCents are tax-INCLUSIVE, matching what the
	// customer was charged. vat='1' on the request is what tells ECPay so.
	UnitPriceCents int64
	AmountCents    int64
}
