package orders

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/invoice/issuer"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

// ErrInvoiceRefused is an invoice write unavailable in the current back-office state.
var ErrInvoiceRefused = errors.New("orders: invoice refused")

// InvoiceReader reads what has been filed for an order.
type InvoiceReader interface {
	Documents(ctx context.Context, orderNumber string) ([]invoice.Document, error)
}

// InvoiceWriter files and changes uniform invoices for the back office. A
// successful method has durably recorded its audit event in the same local
// transaction as the invoice transition.
type InvoiceWriter interface {
	Issue(ctx context.Context, orderNumber string) (invoice.Document, error)
	Void(ctx context.Context, orderNumber, reason string) error
	// FileAllowance relieves the authoritative whole-dollar refunded delta of a live
	// invoice. The provider boundary derives money under a database lock; this
	// consumer supplies only the aggregate and operation identities.
	FileAllowance(ctx context.Context, orderNumber string, operationID uuid.UUID) (invoice.Document, error)
}

var (
	_ InvoiceReader = (*issuer.Store)(nil)
	_ InvoiceWriter = (*issuer.Store)(nil)
)

// fillInvoices puts what has actually been FILED on the order page, which is a
// different question from the preference the customer asked for at checkout.
func (s *Store) fillInvoices(ctx context.Context, view *admin.OrderView, number string) error {
	view.InvoicingEnabled = s.invoiceWriter != nil
	docs, err := s.invoiceReader.Documents(ctx, number)
	if err != nil {
		return err
	}
	for i := range docs {
		d := &docs[i]
		doc := admin.InvoiceDocument{
			Kind: d.Kind, Number: d.Number, ProviderRef: d.ProviderRef,
			AmountCents: d.AmountCents, Status: d.Status,
			IssuedAt: shoptime.Minute(d.IssuedAt),
		}
		for _, l := range d.Lines {
			doc.Lines = append(doc.Lines, admin.InvoiceLine{
				Description: l.Description, Quantity: l.Quantity, AmountCents: l.AmountCents,
			})
		}
		view.InvoiceDocuments = append(view.InvoiceDocuments, doc)
	}
	refunded, err := s.q.SettledRefundsForOrder(ctx, number)
	if err != nil {
		return fmt.Errorf("read settled refunds for %s: %w", number, err)
	}
	view.RefundedCents = refunded
	return s.fillOpenAllowance(ctx, view, number)
}

func (s *Store) fillOpenAllowance(ctx context.Context, view *admin.OrderView, number string) error {
	open, err := s.q.OpenAllowance(ctx, number)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read the open allowance of %s: %w", number, err)
	}
	if open.Status == "pending" {
		view.AllowanceAwaitingUntil = shoptime.Minute(open.LastSendAt.Add(issuer.BuyerConsentWindow))
		return nil
	}
	view.AllowanceAttention = open.LastError
	return nil
}

// IssueInvoice files a uniform invoice for an order. The writer owns the local
// persistence transaction, including its audit row; a second no-op audited
// transaction here would let either half commit without the other.
func (s *Store) IssueInvoice(ctx context.Context, number string) error {
	if s.invoiceWriter == nil {
		return fmt.Errorf("%w: no e-invoice provider is configured", ErrInvoiceRefused)
	}
	filingCtx, err := filingContext(ctx)
	if err != nil {
		return err
	}
	_, err = s.invoiceWriter.Issue(filingCtx, number)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
		pgErr.Code == "23514" && pgErr.ConstraintName == "invoice_issue_itemisation" {
		return fmt.Errorf("%w: invoice itemisation: %w", ErrInvoiceRefused, err)
	}
	return err
}

// FileAllowance files a 折讓 against an order's live invoice, relieving the part
// of the sale that was refunded. A void is for an invoice that should not exist;
// an allowance is for one that should exist for less.
func (s *Store) FileAllowance(
	ctx context.Context, number string, operationID uuid.UUID,
) error {
	if s.invoiceWriter == nil {
		return fmt.Errorf("%w: no e-invoice provider is configured", ErrInvoiceRefused)
	}
	filingCtx, err := filingContext(ctx)
	if err != nil {
		return err
	}
	_, err = s.invoiceWriter.FileAllowance(filingCtx, number, operationID)
	return err
}

func (s *Store) VoidInvoice(ctx context.Context, number, reason string) error {
	if s.invoiceWriter == nil {
		return fmt.Errorf("%w: no e-invoice provider is configured", ErrInvoiceRefused)
	}
	filingCtx, err := filingContext(ctx)
	if err != nil {
		return err
	}
	return s.invoiceWriter.Void(filingCtx, number, reason)
}

func filingContext(ctx context.Context) (context.Context, error) {
	actorID, ok := audit.Actor(ctx)
	if !ok {
		return nil, audit.ErrNoActor
	}
	requestID := web.RequestID(ctx)
	if requestID == "" {
		return nil, audit.ErrNoActor
	}
	return issuer.WithFilingIdentity(ctx, actorID, requestID), nil
}
