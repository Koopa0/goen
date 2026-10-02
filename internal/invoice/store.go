package invoice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/outbox"
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

// Due is an order whose sale became final, so its 統一發票 is owed now:
// 營業稅法 §32's 時限表 invoices a prepaid sale when the money arrives, not at
// dispatch.
type Due struct {
	OrderNumber string `json:"order_number"`
	// Trigger is the provider event that captured the payment, the payment staff
	// attributed, or the checkout that store credit paid in full. It is the
	// system claim's request id.
	Trigger string `json:"trigger"`
}

// EnqueueDue writes the invoice a sale owes in the transaction that took the
// money, keyed on the order so the order is claimed once.
func EnqueueDue(ctx context.Context, q *db.Queries, due Due) error {
	payload, err := json.Marshal(due)
	if err != nil {
		return fmt.Errorf("encode invoice.due: %w", err)
	}
	if err := q.EnqueueMessage(ctx, db.EnqueueMessageParams{
		Topic: outbox.TopicInvoiceDue, DedupeKey: due.OrderNumber, Payload: payload,
	}); err != nil {
		return fmt.Errorf("enqueue invoice.due for order %s: %w", due.OrderNumber, err)
	}
	return nil
}

// ClaimDue records the system's issue for an order; the reconciler sends it.
// It claims with or without a 加值中心, so one configured later still files
// what was owed before it.
func (s *Store) ClaimDue(ctx context.Context, due *Due) error {
	_, err := s.q.ClaimSystemInvoiceIssue(ctx, db.ClaimSystemInvoiceIssueParams{
		OrderNumber: due.OrderNumber, RequestID: due.Trigger,
	})
	return dueClaimOutcome(due.OrderNumber, err)
}

// dueClaimOutcome is nil for a claim made and for one with nothing left to
// claim. Any other refusal is returned, so the message stays queued and
// /admin/health shows it rather than the sale going uninvoiced unseen.
func dueClaimOutcome(orderNumber string, err error) error {
	switch constraintName(err) {
	case "invoice_documents_one_active_invoice_per_order":
		return nil
	case "invoice_issue_itemisation":
		// A sale discounted to nothing has no amount to file.
		return nil
	case "invoice_issue_committed":
		// Cancelled before its claim: the sale it was due for is undone.
		return nil
	}
	if err != nil {
		return fmt.Errorf("claim the invoice order %s owes: %w", orderNumber, err)
	}
	return nil
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
		return mapVoidClaim(live.Number, err)
	}
	_, err = s.processClaim(ctx, operationID)
	return err
}

// mapVoidClaim keeps the two claim_invoice_void rules typed. The handler's
// default arm is a 500 Notice, so wrapping them only would hide a blank
// reason and an already-voided document from the operator.
func mapVoidClaim(documentNumber string, err error) error {
	switch constraintName(err) {
	case "invoice_void_reason":
		return fmt.Errorf("%w: voiding %s needs a reason", ErrReason, documentNumber)
	case "invoice_void_target":
		return fmt.Errorf("%w: %s is not a live invoice", ErrNotFound, documentNumber)
	}
	return fmt.Errorf("claim the void of %s: %w", documentNumber, err)
}

// FileAllowance files a credit note against an order's live invoice. The database
// derives the exact whole-dollar delta from settled refunds and prior
// allowances while holding the original document lock; callers supply no money.
func (s *Store) FileAllowance(
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
