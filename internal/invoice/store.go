package invoice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
)

const (
	// filingTimeout bounds a durable stamp after the browser or provider call has
	// ended. Losing the browser must not lose the operation evidence.
	filingTimeout  = 15 * time.Second
	operationLease = 2 * time.Minute
	reconcilePoll  = 15 * time.Second
)

// Store files what the provider accepted. It runs on the ADMIN pool: `store`
// holds no write on invoice_documents at all.
type Store struct {
	q       *db.Queries
	gateway *Gateway
}

type filingIdentity struct {
	actorID   uuid.UUID
	requestID string
}

type filingIdentityKey struct{}

// WithFilingIdentity carries the authenticated back-office identity across the
// invoice interface without making this provider package depend on account or
// HTTP packages (which would form an import cycle through the UI models).
func WithFilingIdentity(ctx context.Context, actorID uuid.UUID, requestID string) context.Context {
	return context.WithValue(ctx, filingIdentityKey{}, filingIdentity{
		actorID: actorID, requestID: requestID,
	})
}

// NewStore returns a Store over the admin pool.
func NewStore(pool *pgxpool.Pool, gateway *Gateway) *Store {
	if pool == nil || gateway == nil {
		panic("invoice: NewStore requires a pool and a gateway")
	}
	return &Store{q: db.New(pool), gateway: gateway}
}

// filingAuditIdentity is resolved before a provider call. Every successful
// local tax-state transition records this actor and request in the same
// transaction; discovering missing attribution after ECPay accepted would open
// another avoidable remote/local gap.
func filingAuditIdentity(ctx context.Context) (uuid.UUID, string, error) {
	identity, ok := ctx.Value(filingIdentityKey{}).(filingIdentity)
	if !ok || identity.actorID == uuid.Nil {
		return uuid.Nil, "", fmt.Errorf("%w: invoice filing needs a staff actor", ErrRejected)
	}
	if identity.requestID == "" {
		return uuid.Nil, "", fmt.Errorf("%w: invoice filing needs a request id", ErrRejected)
	}
	return identity.actorID, identity.requestID, nil
}

// Enabled reports whether this deployment can issue anything.
func (s *Store) Enabled() bool { return s.gateway.Enabled() }

// Issue durably freezes and claims a request before any provider call. ECPay's
// allocated number is attached only after FetchIssue proves the remote truth.
func (s *Store) Issue(ctx context.Context, orderNumber string) (Document, error) {
	if !s.Enabled() {
		return Document{}, ErrDisabled
	}
	actorID, requestID, err := filingAuditIdentity(ctx)
	if err != nil {
		return Document{}, err
	}
	operationID, err := s.q.ClaimInvoiceIssue(ctx, db.ClaimInvoiceIssueParams{
		OrderNumber: orderNumber, ActorUserID: actorID, RequestID: requestID,
	})
	if err != nil {
		switch constraintName(err) {
		case "invoice_issue_order":
			return Document{}, ErrNotFound
		case "invoice_documents_one_active_invoice_per_order":
			return Document{}, ErrAlreadyIssued
		}
		return Document{}, fmt.Errorf("claim invoice for %s: %w", orderNumber, err)
	}
	return s.processClaim(ctx, operationID)
}

func filingContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), filingTimeout)
}

// Void cancels an issued invoice, at the provider and then here.
func (s *Store) Void(ctx context.Context, orderNumber, reason string) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	actorID, requestID, err := filingAuditIdentity(ctx)
	if err != nil {
		return err
	}
	live, err := s.q.LiveInvoice(ctx, orderNumber)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read the invoice of %s: %w", orderNumber, err)
	}
	operationID, err := s.q.ClaimInvoiceVoid(ctx, db.ClaimInvoiceVoidParams{
		DocumentID: live.ID, Reason: reason,
		ActorUserID: actorID, RequestID: requestID,
	})
	if err != nil {
		return fmt.Errorf("claim the void of %s: %w", live.Number, err)
	}
	_, err = s.processClaim(ctx, operationID)
	return err
}

// Allowance files a credit note against an order's live invoice. The database
// derives the exact whole-dollar delta from settled refunds and prior
// allowances while holding the original document lock; callers supply no money.
func (s *Store) Allowance(
	ctx context.Context, orderNumber string, operationID uuid.UUID,
) (Document, error) {
	if !s.Enabled() {
		return Document{}, ErrDisabled
	}
	actorID, requestID, err := filingAuditIdentity(ctx)
	if err != nil {
		return Document{}, err
	}
	if operationID == uuid.Nil {
		return Document{}, fmt.Errorf("%w: an allowance needs a non-zero operation id",
			ErrRejected)
	}
	live, err := s.q.LiveInvoice(ctx, orderNumber)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Document{}, ErrNotFound
		}
		return Document{}, fmt.Errorf("read the invoice of %s: %w", orderNumber, err)
	}
	operationID, err = s.q.ClaimInvoiceAllowance(ctx, db.ClaimInvoiceAllowanceParams{
		OriginalID: live.ID, OperationID: operationID,
		ActorUserID: actorID, RequestID: requestID,
	})
	if err != nil {
		switch constraintName(err) {
		case "invoice_allowance_within_refund":
			return Document{}, fmt.Errorf("%w: concurrent allowances exhausted the refunded amount", ErrTooMuch)
		case "invoice_operations_one_active_allowance":
			return Document{}, fmt.Errorf("%w: an allowance for order %s is still unresolved",
				ErrClaimed, orderNumber)
		case "invoice_allowance_claim_attribution":
			return Document{}, fmt.Errorf("%w: allowance operation belongs to different facts", ErrRejected)
		}
		return Document{}, fmt.Errorf("claim an allowance for %s: %w", orderNumber, err)
	}
	return s.processClaim(ctx, operationID)
}

func invoiceLineArrays(lines []Line) (
	descriptions []string,
	quantities []int32,
	unitPrices []int64,
	amounts []int64,
) {
	descriptions = make([]string, len(lines))
	quantities = make([]int32, len(lines))
	unitPrices = make([]int64, len(lines))
	amounts = make([]int64, len(lines))
	for i := range lines {
		descriptions[i] = lines[i].Description
		quantities[i] = lines[i].Quantity
		unitPrices[i] = lines[i].UnitPriceCents
		amounts[i] = lines[i].AmountCents
	}
	return descriptions, quantities, unitPrices, amounts
}

// Documents is every filing against one order, for the back office.
func (s *Store) Documents(ctx context.Context, orderNumber string) ([]Document, error) {
	rows, err := s.q.InvoiceDocuments(ctx, orderNumber)
	if err != nil {
		return nil, fmt.Errorf("read the invoices of %s: %w", orderNumber, err)
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}
	lineRows, err := s.q.InvoiceDocumentLines(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read the lines of %s's invoices: %w", orderNumber, err)
	}
	byDocument := make(map[uuid.UUID][]Line, len(rows))
	for i := range lineRows {
		l := &lineRows[i]
		byDocument[l.DocumentID] = append(byDocument[l.DocumentID], Line{
			Description: l.Description, Quantity: l.Quantity,
			UnitPriceCents: l.UnitPriceCents, AmountCents: l.AmountCents,
		})
	}

	out := make([]Document, 0, len(rows))
	for i := range rows {
		d := &rows[i]
		out = append(out, Document{
			ID: d.ID.String(), Kind: d.Kind, Number: d.Number,
			AmountCents: d.AmountCents, Status: d.Status,
			IssuedAt: d.IssuedAt, ProviderRef: d.ProviderRef,
			Lines: byDocument[d.ID],
		})
	}
	return out, nil
}
