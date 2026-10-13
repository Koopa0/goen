// Package issuer issues uniform invoices through ECPay's B2C e-invoice API
// and keeps the durable operations behind them. An unconfigured deployment
// issues nothing and says so; half a configuration does not start. The
// vocabulary it speaks is package invoice.
package issuer

import (
	"errors"

	"github.com/koopa0/goen/internal/invoice"
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

// MaxIssueItems is ECPay's maximum Items array length and therefore the
// highest ItemSeq it can represent. A sale reserves two positions for the
// canonical delivery and whole-dollar adjustment lines.
const (
	MaxIssueItems        = 999
	MaxIssueProductLines = MaxIssueItems - 2
)

// InvoiceCarrier is the 載具 an invoice is filed under, in ECPay's CarrierType
// spelling. It is never the parcel company.
type InvoiceCarrier string

const (
	// InvoiceCarrierNone is a printed invoice or one held in the shop's own account.
	InvoiceCarrierNone          InvoiceCarrier = ""
	InvoiceCarrierMember        InvoiceCarrier = "1"
	InvoiceCarrierMobileBarcode InvoiceCarrier = "3"
)

type OperationKind string

type OperationStatus string

const (
	OperationIssue     OperationKind = "issue"
	OperationAllowance OperationKind = "allowance"
	OperationVoid      OperationKind = "void"

	OperationPending   OperationStatus = "pending"
	OperationAttention OperationStatus = "attention"
	OperationSucceeded OperationStatus = "succeeded"
	OperationRejected  OperationStatus = "rejected"
)

// taxCode is ECPay's ItemTaxType spelling of a tax type: 1 taxable, 3 exempt.
// Zero-rated lines are never sent line by line, so they and anything unknown
// carry no code.
func taxCode(t invoice.TaxType) string {
	switch t {
	case invoice.Taxable:
		return "1"
	case invoice.Exempt:
		return "3"
	default:
		return ""
	}
}
