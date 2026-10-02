// Package ordernotice queues terminal order facts without retaining delivery PII.
package ordernotice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/outbox"
)

// Kind identifies the fact and who initiated a cancellation.
type Kind string

const (
	CancelledByCustomer        Kind = "cancelled_by_customer"
	CancelledByStaff           Kind = "cancelled_by_staff"
	CancelledByPaymentDeadline Kind = "cancelled_by_payment_deadline"
	Delivered                  Kind = "delivered"
	Collected                  Kind = "collected"
)

// Message contains only the order identity and its committed event.
type Message struct {
	OrderID uuid.UUID `json:"order_id"`
	Kind    Kind      `json:"kind"`
	// Refunded says money may have reached the provider for an unpaid order, or
	// went back on a paid one (card or store credit), and has been or will be
	// returned, so the mail must not say nothing was charged. Read in the
	// transaction that cancels the order.
	Refunded bool `json:"refunded"`
}

// Enqueue must use the caller's order transaction so failed transitions send nothing.
func Enqueue(ctx context.Context, q *db.Queries, m Message) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode terminal order notice: %w", err)
	}
	if err := q.EnqueueMessage(ctx, db.EnqueueMessageParams{Topic: outbox.TopicOrderTerminal, DedupeKey: m.OrderID.String() + ":" + string(m.Kind), Payload: payload}); err != nil {
		return fmt.Errorf("enqueue terminal order notice: %w", err)
	}
	return nil
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
