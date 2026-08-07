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

// Store files what the provider accepted.
//
// It runs on the ADMIN pool: issuing is the back office's act, and `store` holds
// no write on invoice_documents at all — a storefront request that could file a
// tax document is a customer issuing their own invoice.
type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
	// gateway may be disabled, in which case Issue answers ErrDisabled and
	// nothing is written. That is the whole shape: no half-filed documents.
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

// Issue files a 統一發票 for an order and records it.
//
// # The ordering, and why it is this way round
//
// The provider is called FIRST and the row written after. The number is theirs
// to allocate, so a row written first would carry one goen invented — and
// invoice_documents_number_present cannot tell the two apart.
//
// That leaves the window this repository already documents for refunds, in the
// recoverable direction: a document filed with the 加值中心 and absent here. It
// is visible (ECPay's own console has it, and the order page shows no invoice)
// and re-issuing is refused by
// invoice_documents_one_active_invoice_per_order, so the failure is a support
// question rather than a second tax filing. The reverse — a row claiming a
// filing that does not exist — is not visible from anywhere.
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
	// A checkout nobody paid for is not a sale. Issuing for one files a tax
	// document for something that did not happen, and undoing that is a
	// correction with the 財政部 rather than a delete.
	if !subject.Committed {
		return Document{}, fmt.Errorf("%w: order %s is not committed; there is no sale to invoice",
			ErrRejected, orderNumber)
	}
	// The unique index refuses a second live invoice anyway. Asking first is
	// what stops goen calling the 加值中心 for a document the database will then
	// refuse to file — which is exactly the orphan the ordering above exists to
	// keep rare.
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
	// Delivery is a line on the invoice, because the customer paid it and a
	// 統一發票 records what was charged. Without it the item total and
	// SalesAmount disagree, which ECPay refuses — and rightly: the difference
	// would be money on the invoice that nothing accounts for.
	if fee := subject.TotalCents - sumLines(lines); fee > 0 {
		lines = append(lines, Line{
			// i18n-exempt: an invoice品名 is filed with the 財政部 and read by a
			// Taiwanese tax authority, not by the visitor. It follows the DOCUMENT's
			// language, which is Chinese for every 統一發票 ever issued.
			Description: "運費", Quantity: 1, UnitPriceCents: fee, AmountCents: fee,
		})
	}

	doc, err := s.gateway.Issue(ctx, IssueRequest{
		// The order number for the FIRST invoice, and a suffix after that.
		// ECPay dedupe on RelateNumber and refuse a repeat, so a reissue after a
		// void — the only way to correct a 統一發票 — needs its own key.
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
//
// Together, because a document without its lines cannot say what was sold — and
// invoice_document_lines exists precisely so the filing is itemised rather than
// a single figure nobody can reconcile against the order.
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

// Void cancels an issued invoice, at the provider and here.
//
// The row is updated only after ECPay accepts, for the reason Issue calls them
// first: a row saying voided while the 加值中心 still holds it live is a claim
// nothing supports, and the 統一發票 platform is the authority on which
// documents exist.
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
	// Zero rows is somebody else voiding it between the read and the write. The
	// provider call is idempotent enough — ECPay refuses a second void — so this
	// is the ordinary loser rather than a failure, and saying so beats a silent
	// success on a document this transaction did not cancel.
	if voided == 0 {
		return fmt.Errorf("%w: invoice %s was voided by somebody else first",
			ErrRejected, live.Number)
	}
	return nil
}

// Allowance files a 折讓 against an order's live invoice.
//
// A refund does not void the invoice: the sale happened and the tax was
// reported, and what changed is that some of it came back. invoice_allowance_valid
// holds the total against the original under a lock, so an allowance that would
// over-relieve is refused by the database rather than by arithmetic here.
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

	// One line, because a 折讓 relieves an AMOUNT rather than particular goods:
	// a partial refund of a three-item order is money back, not two of the three
	// un-sold. Itemising it would invent a breakdown the refund never had.
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
	// ONE query for every document on the page rather than one per row: an order
	// with an invoice and two allowances would otherwise be four round trips to
	// fill one panel.
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

// relateNumber is the 自訂編號 goen sends as ECPay's idempotency key.
//
// The order number alone for the first attempt, so the common case reads as the
// order it is for. A voided invoice frees the slot for a corrected reissue, and
// that reissue needs a DIFFERENT key or ECPay refuses it as a duplicate — which
// is how the staging API taught this, by refusing the second one.
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
