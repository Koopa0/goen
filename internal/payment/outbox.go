package payment

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/outbox"
)

// OrderPaid is what an order.paid message carries. It is deliberately a
// separate type from email.OrderPaid, which decodes it.
type OrderPaid struct {
	Locale      string `json:"locale"`
	OrderNumber string `json:"order_number"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	AmountCents int64  `json:"amount_cents"`
	Card        string `json:"card"`
}

// enqueueOrderPaid writes the receipt in the capture's transaction, keyed on the
// order number so at-least-once delivery produces one receipt.
func enqueueOrderPaid(ctx context.Context, q *db.Queries, orderID uuid.UUID, p *OrderPaid) error {
	to, err := q.OrderRecipient(ctx, orderID)
	if err != nil {
		return fmt.Errorf("read recipient of order %s: %w", p.OrderNumber, err)
	}
	if to.Email == "" {
		// An erased order: nobody to tell, and no failure to report.
		return nil
	}
	// Off the order: a capture runs from a webhook, where nobody is reading.
	p.Email, p.Name, p.Locale = to.Email, to.RecipientName, to.Locale

	payload, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode order.paid: %w", err)
	}
	if err := q.EnqueueMessage(ctx, db.EnqueueMessageParams{
		Topic: outbox.TopicOrderPaid, DedupeKey: p.OrderNumber, Payload: payload,
	}); err != nil {
		return fmt.Errorf("enqueue order.paid: %w", err)
	}
	return nil
}
