// Package notice queues terminal order facts without retaining delivery PII,
// and delivers each to the recipient the order holds when it is sent.
package notice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/outbox"
)

// Enqueue must use the caller's order transaction so failed transitions send nothing.
func Enqueue(ctx context.Context, q *db.Queries, m *outbox.OrderTerminal) error {
	return outbox.Enqueue(ctx, q, outbox.TopicOrderTerminal, m.OrderID.String()+":"+string(m.Kind), m)
}

// Recipient is who a notice goes to, as the order holds it at delivery time.
type Recipient struct {
	Address, Name, Locale, OrderNumber string
	// RescissionEnds is the last day to return what was delivered; the zero time
	// before any delivery.
	RescissionEnds time.Time
}

type Recipients struct{ q *db.Queries }

func NewRecipients(conn db.DBTX) Recipients { return Recipients{q: db.New(conn)} }

// Of returns the order's current recipient, and false when there is none: the
// row is erased or gone, and sending is what must not happen.
func (r Recipients) Of(ctx context.Context, orderID uuid.UUID) (Recipient, bool, error) {
	to, err := r.q.TerminalOrderRecipient(ctx, orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Recipient{}, false, nil
	}
	if err != nil {
		return Recipient{}, false, fmt.Errorf("read terminal order recipient: %w", err)
	}
	recipient := Recipient{Address: to.Email.String, Name: to.RecipientName.String, Locale: to.Locale, OrderNumber: to.OrderNumber}
	if to.Delivered {
		recipient.RescissionEnds = to.RescissionEnds
	}
	return recipient, true, nil
}

// Deliver sends a terminal notice to the order's recipient as the order holds
// it now. An order whose recipient is erased or gone completes without
// sending: rescheduling would retry exactly what must not happen. A failed read
// or send stays retryable.
func (r Recipients) Deliver(ctx context.Context, m *outbox.OrderTerminal,
	send func(context.Context, *outbox.OrderTerminal, Recipient) error) error {
	to, ok, err := r.Of(ctx, m.OrderID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return send(ctx, m, to)
}

// EnqueuePartRefunded must use the transaction that settles the refund, so the
// letter is written exactly when the units are given up.
func EnqueuePartRefunded(ctx context.Context, q *db.Queries, m *outbox.OrderPartRefunded) error {
	return outbox.Enqueue(ctx, q, outbox.TopicOrderPartRefunded, m.ReturnRequestID.String(), m)
}

// PartRefund is what the letter for a settled unsent refund says, as the order
// and its refund hold it at delivery.
type PartRefund struct {
	Address, Name, Locale, OrderNumber string
	Items                              []PartRefundItem
	// RefundCents is CardCents + CreditCents: the refund's frozen split.
	RefundCents, CardCents, CreditCents int64
}

// PartRefundItem is one line of the refund: the snapshot the order bought.
type PartRefundItem struct {
	Name, Label string
	Quantity    int32
}

// PartRefund reads the letter for a settled unsent refund at delivery, and
// false when the order has no recipient any more.
func (r Recipients) PartRefund(ctx context.Context, m *outbox.OrderPartRefunded) (PartRefund, bool, error) {
	to, ok, err := r.Of(ctx, m.OrderID)
	if err != nil || !ok {
		return PartRefund{}, false, err
	}
	rows, err := r.q.PartRefundLines(ctx, db.PartRefundLinesParams{
		ReturnRequestID: m.ReturnRequestID, OrderID: m.OrderID,
	})
	if err != nil {
		return PartRefund{}, false, fmt.Errorf("read unsent refund %s: %w", m.ReturnRequestID, err)
	}
	// Enqueued by the transaction that completed it, so a refund with no
	// lines here is one the letter must not describe: Stuck() shows it.
	if len(rows) == 0 {
		return PartRefund{}, false, fmt.Errorf("no settled unsent refund %s on order %s",
			m.ReturnRequestID, m.OrderID)
	}
	refund := PartRefund{
		Address: to.Address, Name: to.Name, Locale: to.Locale, OrderNumber: to.OrderNumber,
		RefundCents: rows[0].RefundCents, CardCents: rows[0].CardCents, CreditCents: rows[0].CreditCents,
	}
	for _, row := range rows {
		refund.Items = append(refund.Items, PartRefundItem{
			Name: row.ProductName, Label: row.VariantLabel, Quantity: row.Quantity,
		})
	}
	return refund, true, nil
}

// DeliverPartRefunded sends the letter for a settled unsent refund, read at
// delivery from the order and the refund: the queued row names neither address
// nor amount. An order whose recipient is erased or gone completes without
// sending; a refund with no settled lines, a failed read or a failed send stays
// retryable.
func (r Recipients) DeliverPartRefunded(ctx context.Context, m *outbox.OrderPartRefunded,
	send func(context.Context, *outbox.OrderPartRefunded, *PartRefund) error) error {
	refund, ok, err := r.PartRefund(ctx, m)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return send(ctx, m, &refund)
}
