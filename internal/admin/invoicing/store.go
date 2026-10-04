// Package invoicing files, voids and corrects an order's uniform invoice from the
// order page, and shows that page what has been filed.
package invoicing

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

// ErrRefused is an invoice write asked of a back office with no e-invoice
// provider configured.
var ErrRefused = errors.New("invoicing: refused")

type Reader interface {
	Documents(ctx context.Context, orderNumber string) ([]invoice.Document, error)
}

// Writer files and changes uniform invoices for the back office. A
// successful method has durably recorded its audit event in the same local
// transaction as the invoice transition.
type Writer interface {
	Issue(ctx context.Context, orderNumber string) (invoice.Document, error)
	Void(ctx context.Context, orderNumber, reason string) error
	// FileAllowance relieves the authoritative whole-dollar refunded delta of a live
	// invoice. The provider boundary derives money under a database lock; this
	// consumer supplies only the aggregate and operation identities.
	FileAllowance(ctx context.Context, orderNumber string, operationID uuid.UUID) (invoice.Document, error)
}

var (
	_ Reader = (*invoice.Store)(nil)
	_ Writer = (*invoice.Store)(nil)
)

type Store struct {
	q *db.Queries
	// Both may be nil when no provider is configured.
	reader Reader
	writer Writer
}

func NewStore(pool *pgxpool.Pool, reader Reader, writer Writer) *Store {
	if pool == nil {
		panic("invoicing: NewStore requires a pool")
	}
	if (reader == nil) != (writer == nil) {
		panic("invoicing: NewStore requires both invoice dependencies or neither")
	}
	return &Store{q: db.New(pool), reader: reader, writer: writer}
}

// FillOrder puts what has actually been FILED on the order page, which is a
// different question from the preference the customer asked for at checkout.
func (s *Store) FillOrder(ctx context.Context, view *admin.OrderView, number string) error {
	if s.reader == nil {
		return nil
	}
	view.InvoicingEnabled = true
	docs, err := s.reader.Documents(ctx, number)
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
		view.AllowanceAwaitingUntil = shoptime.Minute(open.LastSendAt.Add(invoice.BuyerConsentWindow))
		return nil
	}
	view.AllowanceAttention = open.LastError
	return nil
}

// Issue files a uniform invoice for an order. The writer owns the local
// persistence transaction, including its audit row; a second no-op audited
// transaction here would let either half commit without the other.
func (s *Store) Issue(ctx context.Context, number string) error {
	if s.writer == nil {
		return fmt.Errorf("%w: no e-invoice provider is configured", ErrRefused)
	}
	filingCtx, err := filingContext(ctx)
	if err != nil {
		return err
	}
	_, err = s.writer.Issue(filingCtx, number)
	return err
}

// Allow files a 折讓 against an order's live invoice, relieving the part
// of the sale that was refunded. A void is for an invoice that should not exist;
// an allowance is for one that should exist for less.
func (s *Store) Allow(
	ctx context.Context, number string, operationID uuid.UUID,
) error {
	if s.writer == nil {
		return fmt.Errorf("%w: no e-invoice provider is configured", ErrRefused)
	}
	filingCtx, err := filingContext(ctx)
	if err != nil {
		return err
	}
	_, err = s.writer.FileAllowance(filingCtx, number, operationID)
	return err
}

func (s *Store) Void(ctx context.Context, number, reason string) error {
	if s.writer == nil {
		return fmt.Errorf("%w: no e-invoice provider is configured", ErrRefused)
	}
	filingCtx, err := filingContext(ctx)
	if err != nil {
		return err
	}
	return s.writer.Void(filingCtx, number, reason)
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
	return invoice.WithFilingIdentity(ctx, actorID, requestID), nil
}
