package invoice

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
)

// Store files what the provider accepted. It runs on the ADMIN pool: `store`
// holds no write on invoice_documents at all.
type Store struct {
	pool    *pgxpool.Pool
	q       *db.Queries
	gateway *Gateway
}

// NewStore returns a Store over the admin pool.
func NewStore(pool *pgxpool.Pool, gateway *Gateway) *Store {
	if pool == nil || gateway == nil {
		panic("invoice: NewStore requires a pool and a gateway")
	}
	return &Store{pool: pool, q: db.New(pool), gateway: gateway}
}

// Enabled reports whether this deployment can issue anything.
func (s *Store) Enabled() bool { return s.gateway.Enabled() }

// Issue files a uniform invoice for an order and records it. The provider is
// called FIRST and the row written after: the number is theirs to allocate.
func (s *Store) Issue(ctx context.Context, orderNumber string) (Document, error) {
	if !s.Enabled() {
		return Document{}, ErrDisabled
	}

	subject, err := s.q.InvoiceSubject(ctx, orderNumber)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Document{}, ErrNotFound
		}
		return Document{}, fmt.Errorf("read order %s: %w", orderNumber, err)
	}
	if !subject.Committed {
		return Document{}, fmt.Errorf("%w: order %s is not committed; there is no sale to invoice",
			ErrRejected, orderNumber)
	}
	// The unique index refuses a second live invoice anyway; asking first is
	// what stops goen filing one the database will then refuse to record.
	if _, liveErr := s.q.LiveInvoice(ctx, orderNumber); liveErr == nil {
		return Document{}, ErrAlreadyIssued
	} else if !errors.Is(liveErr, pgx.ErrNoRows) {
		return Document{}, fmt.Errorf("check for an existing invoice: %w", liveErr)
	}

	rows, err := s.q.InvoiceSubjectLines(ctx, subject.ID)
	if err != nil {
		return Document{}, fmt.Errorf("read the lines of %s: %w", orderNumber, err)
	}
	lines := make([]Line, 0, len(rows)+1)
	for i := range rows {
		l := &rows[i]
		name := l.ProductName
		if l.VariantLabel.Valid && l.VariantLabel.String != "" {
			name += " " + l.VariantLabel.String
		}
		lines = append(lines, Line{
			Description: name, Quantity: l.Quantity,
			UnitPriceCents: l.UnitPriceCents, AmountCents: l.AmountCents,
		})
	}
	// Delivery is a line, or the itemisation and SalesAmount disagree by exactly
	// the shipping fee and ECPay refuses the document.
	if fee := subject.TotalCents - sumLines(lines); fee > 0 {
		lines = append(lines, Line{
			// i18n-exempt: an invoice品名 is filed with the 財政部 and read by a
			// Taiwanese tax authority, not by the visitor. It follows the DOCUMENT's
			// language, which is Chinese for every 統一發票 ever issued.
			Description: "運費", Quantity: 1, UnitPriceCents: fee, AmountCents: fee,
		})
	}

	doc, err := s.gateway.Issue(ctx, IssueRequest{
		OrderNumber:  relateNumber(subject.OrderNumber, subject.Attempt),
		CustomerName: subject.CustomerName,
		Email:        subject.Email,
		Preference:   subject.InvoiceType,
		CarrierCode:  subject.CarrierCode,
		TaxID:        subject.TaxID,
		AmountCents:  subject.TotalCents,
		Lines:        lines,
	})
	if err != nil {
		return Document{}, err
	}

	if err := s.file(ctx, subject.ID, uuid.NullUUID{}, doc, lines); err != nil {
		return Document{}, err
	}
	return doc, nil
}

// file records an accepted document and its lines in one transaction.
func (s *Store) file(
	ctx context.Context, orderID uuid.UUID, original uuid.NullUUID, doc Document, lines []Line,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin invoice filing: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	id, err := q.RecordInvoiceDocument(ctx, db.RecordInvoiceDocumentParams{
		OrderID: orderID, Kind: doc.Kind, OriginalID: original,
		Number: doc.Number, AmountCents: doc.AmountCents,
		ProviderRef: doc.ProviderRef, IssuedAt: doc.IssuedAt,
	})
	if err != nil {
		return fmt.Errorf("file %s %s: %w", doc.Kind, doc.Number, err)
	}
	for i, l := range lines {
		if lineErr := q.RecordInvoiceLine(ctx, db.RecordInvoiceLineParams{
			DocumentID: id, Description: l.Description, Quantity: l.Quantity,
			UnitPriceCents: l.UnitPriceCents, AmountCents: l.AmountCents,
			Position: int32(i),
		}); lineErr != nil {
			return fmt.Errorf("file line %d of %s: %w", i, doc.Number, lineErr)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit invoice filing: %w", err)
	}
	return nil
}

// Void cancels an issued invoice, at the provider and then here.
func (s *Store) Void(ctx context.Context, orderNumber, reason string) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	live, err := s.q.LiveInvoice(ctx, orderNumber)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read the invoice of %s: %w", orderNumber, err)
	}
	if voidErr := s.gateway.Void(ctx, live.Number, reason); voidErr != nil {
		return voidErr
	}
	voided, err := s.q.VoidInvoiceDocument(ctx, live.ID)
	if err != nil {
		return fmt.Errorf("record the void of %s: %w", live.Number, err)
	}
	// Zero rows is somebody else voiding it between the read and the write.
	if voided == 0 {
		return fmt.Errorf("%w: invoice %s was voided by somebody else first",
			ErrRejected, live.Number)
	}
	return nil
}

// Allowance files a credit note against an order's live invoice.
// invoice_allowance_valid holds the total against the original under a lock, so
// an over-relieving allowance is refused there rather than by arithmetic here.
func (s *Store) Allowance(ctx context.Context, orderNumber string, amountCents int64) (Document, error) {
	if !s.Enabled() {
		return Document{}, ErrDisabled
	}
	if amountCents <= 0 {
		return Document{}, fmt.Errorf("%w: an allowance for %d relieves nothing",
			ErrRejected, amountCents)
	}

	subject, err := s.q.InvoiceSubject(ctx, orderNumber)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Document{}, ErrNotFound
		}
		return Document{}, fmt.Errorf("read order %s: %w", orderNumber, err)
	}
	live, err := s.q.LiveInvoice(ctx, orderNumber)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Document{}, ErrNotFound
		}
		return Document{}, fmt.Errorf("read the invoice of %s: %w", orderNumber, err)
	}

	// One line, because a credit note relieves an AMOUNT rather than particular
	// goods; itemising it would invent a breakdown the refund never had.
	lines := []Line{{
		// i18n-exempt: see the 運費 line in Issue — a 折讓 品名 is filed with the
		// 財政部 and is Chinese by the document's own nature.
		Description: "退貨折讓", Quantity: 1,
		UnitPriceCents: amountCents, AmountCents: amountCents,
	}}

	doc, err := s.gateway.Allowance(ctx, AllowanceRequest{
		InvoiceNumber: live.Number,
		InvoiceDate:   live.IssuedAt,
		CustomerName:  subject.CustomerName,
		Email:         subject.Email,
		AmountCents:   amountCents,
		Lines:         lines,
	})
	if err != nil {
		return Document{}, err
	}

	if err := s.file(ctx, subject.ID,
		uuid.NullUUID{UUID: live.ID, Valid: true}, doc, lines); err != nil {
		return Document{}, err
	}
	return doc, nil
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

// relateNumber is ECPay's idempotency key. A reissue after a void needs a
// DIFFERENT one or they refuse it as a duplicate.
func relateNumber(orderNumber string, attempt int32) string {
	if attempt <= 0 {
		return orderNumber
	}
	return orderNumber + "-" + strconv.FormatInt(int64(attempt), 10)
}

// sumLines is what the itemisation comes to.
func sumLines(lines []Line) int64 {
	var n int64
	for _, l := range lines {
		n += l.AmountCents
	}
	return n
}
