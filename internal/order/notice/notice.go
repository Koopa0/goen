// Package notice queues terminal order facts without retaining delivery PII.
package notice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/outbox"
)

// Enqueue must use the caller's order transaction so failed transitions send nothing.
func Enqueue(ctx context.Context, q *db.Queries, m *email.OrderTerminal) error {
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

// EnqueuePartRefunded must use the transaction that settles the refund, so the
// letter is written exactly when the units are given up.
func EnqueuePartRefunded(ctx context.Context, q *db.Queries, m *email.OrderPartRefunded) error {
	return outbox.Enqueue(ctx, q, outbox.TopicOrderPartRefunded, m.ReturnRequestID.String(), m)
}

// PartRefund is the letter for a settled unsent refund, read at delivery, and
// false when the order has no recipient any more.
func (r Recipients) PartRefund(ctx context.Context, m *email.OrderPartRefunded) (email.PartRefundLetter, bool, error) {
	to, ok, err := r.Of(ctx, m.OrderID)
	if err != nil || !ok {
		return email.PartRefundLetter{}, false, err
	}
	rows, err := r.q.PartRefundLines(ctx, db.PartRefundLinesParams{
		ReturnRequestID: m.ReturnRequestID, OrderID: m.OrderID,
	})
	if err != nil {
		return email.PartRefundLetter{}, false, fmt.Errorf("read unsent refund %s: %w", m.ReturnRequestID, err)
	}
	// Enqueued by the transaction that completed it, so a refund with no
	// lines here is one the letter must not describe: Stuck() shows it.
	if len(rows) == 0 {
		return email.PartRefundLetter{}, false, fmt.Errorf("no settled unsent refund %s on order %s",
			m.ReturnRequestID, m.OrderID)
	}
	letter := email.PartRefundLetter{
		Address: to.Address, Name: to.Name, Locale: to.Locale, OrderNumber: to.OrderNumber,
		RefundCents: rows[0].RefundCents, CardCents: rows[0].CardCents, CreditCents: rows[0].CreditCents,
	}
	for _, row := range rows {
		letter.Items = append(letter.Items, email.PartRefundItem{
			Name: row.ProductName, Label: row.VariantLabel, Quantity: row.Quantity,
		})
	}
	return letter, true, nil
}
