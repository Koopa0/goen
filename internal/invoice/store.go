package invoice

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
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
	// Delivery is a line of its OWN figure, not the residual. It used to be
	// total - sum(lines), which is shipping MINUS discount: on a discounted
	// order that also qualified for 免運 the residual is negative, no line is
	// appended, and ECPay refuses the document outright (5000022 「與商品合計
	// 金額不符」) — so those orders could not be invoiced by any path. When the
	// fee was the larger of the two it was worse, because it SUCCEEDED: a
	// 統一發票 filed with the 財政部 stating a carriage charge nobody paid, with
	// the discount invisible.
	// The discount comes off the ITEMS and never the delivery: a coupon is capped
	// at the subtotal, because one that could eat the shipping fee would drive
	// the order negative. It rides on the items rather than as a negative line
	// because ECPay's item amounts are unsigned, and because a 統一發票 records
	// what each thing was actually sold for.
	lines = discountLines(lines, subject.DiscountCents)
	if subject.ShippingCents > 0 {
		lines = append(lines, Line{
			// i18n-exempt: an invoice品名 is filed with the 財政部 and read by a
			// Taiwanese tax authority, not by the visitor. It follows the DOCUMENT's
			// language, which is Chinese for every 統一發票 ever issued.
			Description: "運費", Quantity: 1,
			UnitPriceCents: subject.ShippingCents, AmountCents: subject.ShippingCents,
		})
	}
	// And snapped to whole dollars against the header, because the document is
	// filed in dollars and each side used to be rounded independently: two lines
	// of NT$1.50 truncate to 1 each while their 3.00 header truncates to 3, and
	// a percentage coupon makes fractional cents ordinary — 15% of NT$999 leaves
	// the header at 849 beside an item of 999.
	lines = snapToDollars(lines, subject.TotalCents)

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
// discountLines spreads a discount across the item lines so that the whole
// itemisation still sums to what was charged.
//
// Largest-remainder: each line takes its proportional share rounded down, and
// the cents left over go to the lines with the largest remainders, one each.
// That keeps every amount unsigned, keeps the sum exact, and puts any rounding
// error where it is smallest relative to the line.
func discountLines(lines []Line, discountCents int64) []Line {
	if discountCents <= 0 || len(lines) == 0 {
		return lines
	}
	total := sumLines(lines)
	if total <= 0 {
		return lines
	}
	if discountCents >= total {
		// Nothing was charged for the goods. The document still has to balance,
		// so every line goes to zero rather than negative.
		for i := range lines {
			lines[i].AmountCents = 0
			lines[i].UnitPriceCents = 0
		}
		return lines
	}

	type share struct {
		at        int
		remainder int64
	}
	shares := make([]share, 0, len(lines))
	var given int64
	for i := range lines {
		exact := lines[i].AmountCents * discountCents
		cut := exact / total
		lines[i].AmountCents -= cut
		given += cut
		shares = append(shares, share{at: i, remainder: exact % total})
	}
	slices.SortFunc(shares, func(a, b share) int { return cmp.Compare(b.remainder, a.remainder) })
	for i := 0; given < discountCents; i++ {
		lines[shares[i%len(shares)].at].AmountCents--
		given++
	}

	// The unit price follows the amount, or the document says a quantity times a
	// price that is not the amount beside it.
	for i := range lines {
		if lines[i].Quantity > 0 {
			lines[i].UnitPriceCents = lines[i].AmountCents / int64(lines[i].Quantity)
		}
	}
	return lines
}

// snapToDollars rounds every line to a whole number of dollars so that the
// itemisation sums to the header in the units the document is actually filed
// in. Largest-remainder again, and the header itself is truncated because that
// is what the customer's own total rounds to.
//
// The unit price is derived from the snapped amount rather than snapped on its
// own: ECPay files ItemPrice beside ItemAmount, and a price times a count that
// does not equal the amount next to it is a document that contradicts itself.
func snapToDollars(lines []Line, headerCents int64) []Line {
	if len(lines) == 0 {
		return lines
	}
	wantDollars := headerCents / 100

	type share struct {
		at        int
		remainder int64
	}
	shares := make([]share, 0, len(lines))
	var given int64
	for i := range lines {
		// The remainder is taken BEFORE the truncation it is the remainder OF.
		shares = append(shares, share{at: i, remainder: lines[i].AmountCents % 100})
		d := lines[i].AmountCents / 100
		lines[i].AmountCents = d * 100
		given += d
	}
	slices.SortFunc(shares, func(a, b share) int { return cmp.Compare(b.remainder, a.remainder) })
	for i := 0; given < wantDollars && len(shares) > 0; i++ {
		lines[shares[i%len(shares)].at].AmountCents += 100
		given++
	}
	for i := range lines {
		if lines[i].Quantity > 0 {
			lines[i].UnitPriceCents = lines[i].AmountCents / int64(lines[i].Quantity)
		}
	}
	return lines
}

func sumLines(lines []Line) int64 {
	var n int64
	for _, l := range lines {
		n += l.AmountCents
	}
	return n
}
