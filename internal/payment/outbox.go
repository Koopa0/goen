package payment

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/outbox"
)

// OrderPaid is what an order.paid message carries. Mirrored in internal/email,
// which decodes it — see the note on cart.OrderPlaced for why the two are not
// one type.
type OrderPaid struct {
	// Locale must match email.OrderPaid. The two structs are
	// deliberately separate — a consumer that imports the producer's types
	// cannot be deployed a version behind — and TestEveryMailPayloadMatchesItsProducer
	// is what stops the copies drifting.
	Locale      string `json:"locale"`
	OrderNumber string `json:"order_number"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	AmountCents int64  `json:"amount_cents"`
	Card        string `json:"card"`
}

// enqueueOrderPaid writes the receipt in the CAPTURE's transaction.
//
// That is the whole reason it exists rather than a send after the webhook
// returns: the money moving and the promise to tell somebody about it commit
// together. Sent afterwards, a process that dies in between takes money and
// says nothing.
//
// dedupe_key is the order number, so Stripe's at-least-once delivery produces
// one receipt — the same protection the points award relies on.
func enqueueOrderPaid(ctx context.Context, q *db.Queries, orderID uuid.UUID, p *OrderPaid) error {
	to, err := q.OrderRecipient(ctx, orderID)
	if err != nil {
		return fmt.Errorf("read recipient of order %s: %w", p.OrderNumber, err)
	}
	if to.Email == "" {
		// An erased order. There is nobody to tell and no failure to report:
		// erasure is the customer asking not to be contacted.
		return nil
	}
	// The locale comes off the ORDER, not off this request: a capture runs from
	// a Stripe webhook, where nobody is reading anything.
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
