// Package ordernotice queues terminal order facts without retaining delivery PII.
package ordernotice

import (
	"context"
	"errors"
	"fmt"

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
	// RescissionEnds is the last day to return what was delivered, or "".
	RescissionEnds string
}

// Recipients reads them.
type Recipients struct{ q *db.Queries }

// NewRecipients reads through the given connection.
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
	return Recipient{Address: to.Email.String, Name: to.RecipientName.String, Locale: to.Locale, OrderNumber: to.OrderNumber, RescissionEnds: to.RescissionEnds}, true, nil
}
