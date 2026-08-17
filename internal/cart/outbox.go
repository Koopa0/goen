package cart

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
)

// OrderPlaced is what an order.placed message carries. The email address travels
// in the payload rather than being looked up at delivery time, because erase_user
// blanks order_private_data.
type OrderPlaced struct {
	// Every field must match email.OrderPlaced, which is a deliberately separate
	// copy so a consumer cannot be deployed a version behind its producer.
	Locale      string `json:"locale"`
	OrderNumber string `json:"order_number"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	TotalCents  int64  `json:"total_cents"`
}

// enqueueOrderPlaced writes the message in the order's own transaction. The
// dedupe key is the order number, so a retried checkout cannot produce a second
// confirmation email.
func enqueueOrderPlaced(ctx context.Context, q *db.Queries, orderID uuid.UUID, number string, addr *Address, totalCents int64) error {
	payload, err := json.Marshal(OrderPlaced{
		OrderNumber: number, Email: addr.Email, Name: addr.Name, TotalCents: totalCents,
		// The visitor is right here; later messages about this order have no
		// request and read orders.locale, which this statement writes beside it.
		Locale: i18n.FromContext(ctx).Tag(),
	})
	if err != nil {
		return fmt.Errorf("encode order.placed: %w", err)
	}
	if err := q.EnqueueMessage(ctx, db.EnqueueMessageParams{
		Topic: outbox.TopicOrderPlaced, DedupeKey: number, Payload: payload,
	}); err != nil {
		return fmt.Errorf("enqueue order.placed: %w", err)
	}
	_ = orderID
	return nil
}
