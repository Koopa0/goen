package payment

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/outbox"
)

// enqueueOrderPaid writes the receipt in the capture's transaction, keyed on the
// order number so at-least-once delivery produces one receipt.
func enqueueOrderPaid(ctx context.Context, q *db.Queries, orderID uuid.UUID, p *email.OrderPaid) error {
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

	return outbox.Enqueue(ctx, q, outbox.TopicOrderPaid, p.OrderNumber, p)
}
