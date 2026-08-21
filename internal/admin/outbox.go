package admin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/outbox"
)

// OrderShipped is what an order.shipped message carries, a separate copy of
// email.OrderShipped so a consumer may lag a version.
type OrderShipped struct {
	Locale      string `json:"locale"`
	OrderNumber string `json:"order_number"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Carrier     string `json:"carrier"`
	Tracking    string `json:"tracking"`
}

// RestockNotice is what a catalogue.restocked message carries.
type RestockNotice struct {
	Locale      string `json:"locale"`
	Email       string `json:"email"`
	ProductName string `json:"product_name"`
	Slug        string `json:"slug"`
	SKU         string `json:"sku"`
}

func enqueueOrderShipped(ctx context.Context, q *db.Queries, orderID uuid.UUID, m *OrderShipped) error {
	to, err := q.ShipmentRecipient(ctx, orderID)
	if err != nil {
		return fmt.Errorf("read recipient of order %s: %w", m.OrderNumber, err)
	}
	if to.Email == "" {
		return nil
	}
	// The CUSTOMER's language, off the order, never the staff member's.
	m.Email, m.Name, m.Locale = to.Email, to.RecipientName, to.Locale

	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode order.shipped: %w", err)
	}
	// Keyed on the CARRIER and the tracking number together, which is what
	// order_shipments is unique on. The tracking number alone is narrower than
	// the shipment's own key: two carriers may legitimately issue the same
	// number, and the second dispatch then met ON CONFLICT DO NOTHING — Ship
	// still succeeded, and a customer was never told their parcel had left.
	// An order in two parcels is still two notices, because two parcels of one
	// order carry two tracking numbers.
	if err := q.EnqueueMessage(ctx, db.EnqueueMessageParams{
		Topic:     outbox.TopicOrderShipped,
		DedupeKey: m.Carrier + ":" + m.Tracking,
		Payload:   payload,
	}); err != nil {
		return fmt.Errorf("enqueue order.shipped: %w", err)
	}
	return nil
}

// The claim and the enqueue must commit together, or somebody is marked told and
// the partial index stops them asking again.
func enqueueRestockNotices(ctx context.Context, q *db.Queries, variantID uuid.UUID) error {
	claimed, err := q.ClaimRestockNotices(ctx, variantID)
	if err != nil {
		return fmt.Errorf("claim restock notices: %w", err)
	}
	if len(claimed) == 0 {
		return nil
	}

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
			Locale: c.Locale,
		})
		if encErr != nil {
			return fmt.Errorf("encode restock notice: %w", encErr)
		}
		if err := q.EnqueueMessage(ctx, db.EnqueueMessageParams{
			Topic: outbox.TopicRestocked, DedupeKey: c.ID.String(), Payload: payload,
		}); err != nil {
			return fmt.Errorf("enqueue restock notice: %w", err)
		}
	}
	return nil
}
