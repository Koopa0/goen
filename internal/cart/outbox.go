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

// OrderPlaced is what an order.placed message carries.
//
// The email address is in the payload rather than looked up at delivery time,
// and that is deliberate: erase_user blanks order_private_data, so a message
// delivered after an erasure would otherwise have nowhere to go. Carrying it
// means the message is a snapshot of what was true when the order was placed,
// which is what a receipt is.
type OrderPlaced struct {
	// Locale must match email.OrderPlaced. The two structs are
	// deliberately separate — a consumer that imports the producer's types
	// cannot be deployed a version behind — and TestEveryMailPayloadMatchesItsProducer
	// is what stops the copies drifting.
	Locale      string `json:"locale"`
	OrderNumber string `json:"order_number"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	TotalCents  int64  `json:"total_cents"`
}

// enqueueOrderPlaced writes the message in the order's own transaction.
//
// dedupe_key is the order number, so a retried checkout under the same
// idempotency key cannot produce a second confirmation email.
func enqueueOrderPlaced(ctx context.Context, q *db.Queries, orderID uuid.UUID, number string, addr *Address, totalCents int64) error {
	payload, err := json.Marshal(OrderPlaced{
		OrderNumber: number, Email: addr.Email, Name: addr.Name, TotalCents: totalCents,
		// The visitor is right here, so the request's own locale is the answer.
		// The later messages about this order cannot do that and read
		// orders.locale instead — which is the same value, written by the
		// statement this one runs beside.
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
