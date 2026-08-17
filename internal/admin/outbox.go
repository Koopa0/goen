package admin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/outbox"
)

// OrderShipped is what an order.shipped message carries.
//
// Deliberately a separate copy of email.OrderShipped, held to it field for field
// by TestEveryMailPayloadMatchesItsProducer.
type OrderShipped struct {
	Locale      string `json:"locale"`
	OrderNumber string `json:"order_number"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Carrier     string `json:"carrier"`
	Tracking    string `json:"tracking"`
}

// RestockNotice is what a catalogue.restocked message carries.
//
// Deliberately a separate copy of email.RestockNotice, held to it field for field
// by TestEveryMailPayloadMatchesItsProducer.
type RestockNotice struct {
	Locale      string `json:"locale"`
	Email       string `json:"email"`
	ProductName string `json:"product_name"`
	Slug        string `json:"slug"`
	SKU         string `json:"sku"`
}

// enqueueOrderShipped writes the dispatch notice in Ship's own transaction.
func enqueueOrderShipped(ctx context.Context, q *db.Queries, orderID uuid.UUID, m *OrderShipped) error {
	to, err := q.ShipmentRecipient(ctx, orderID)
	if err != nil {
		return fmt.Errorf("read recipient of order %s: %w", m.OrderNumber, err)
	}
	if to.Email == "" {
		// An erased order — there is nobody to tell, and that is not a failure.
		return nil
	}
	// The CUSTOMER's language, off the order, never the staff member's.
	m.Email, m.Name, m.Locale = to.Email, to.RecipientName, to.Locale

	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode order.shipped: %w", err)
	}
	// Keyed on the TRACKING number rather than the order: an order shipped in
	// two parcels is two notices.
	if err := q.EnqueueMessage(ctx, db.EnqueueMessageParams{
		Topic: outbox.TopicOrderShipped, DedupeKey: m.Tracking, Payload: payload,
	}); err != nil {
		return fmt.Errorf("enqueue order.shipped: %w", err)
	}
	return nil
}

// enqueueRestockNotices claims every pending notice for a variant and enqueues
// one message each, in the caller's transaction.
//
// The claim and the enqueue must commit together: claimed without enqueuing, the
// customer is marked told, and the partial unique index stops them asking again.
// A movement that does not cross the threshold claims nothing, so this is safe
// to call on every adjustment.
func enqueueRestockNotices(ctx context.Context, q *db.Queries, variantID uuid.UUID) error {
	claimed, err := q.ClaimRestockNotices(ctx, variantID)
	if err != nil {
		return fmt.Errorf("claim restock notices: %w", err)
	}
	if len(claimed) == 0 {
		return nil
	}

	// One read per LOCALE, not per recipient: the product name has to be in each
	// reader's language, and there are two languages and dozens of subscribers.
	subjects := make(map[string]db.RestockSubjectRow, 2)
	for _, c := range claimed {
		if _, ok := subjects[c.Locale]; ok {
			continue
		}
		subject, subErr := q.RestockSubject(ctx, db.RestockSubjectParams{
			VariantID: variantID, Locale: c.Locale,
		})
		if subErr != nil {
			return fmt.Errorf("read restock subject in %s: %w", c.Locale, subErr)
		}
		subjects[c.Locale] = subject
	}

	for _, c := range claimed {
		subject := subjects[c.Locale]
		payload, encErr := json.Marshal(RestockNotice{
			Email: c.Email, ProductName: subject.ProductName,
			Slug: subject.Slug, SKU: subject.SKU,
			// Recorded when they asked: the request that got here is the
			// shop's, so there is no reader's locale to read.
			Locale: c.Locale,
		})
		if encErr != nil {
			return fmt.Errorf("encode restock notice: %w", encErr)
		}
		// Keyed on the notification row, already spent by the claim above, so a
		// retried adjustment cannot produce a second mail.
		if err := q.EnqueueMessage(ctx, db.EnqueueMessageParams{
			Topic: outbox.TopicRestocked, DedupeKey: c.ID.String(), Payload: payload,
		}); err != nil {
			return fmt.Errorf("enqueue restock notice: %w", err)
		}
	}
	return nil
}
