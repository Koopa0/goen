package admin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/destination"
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
	Pickup      bool   `json:"pickup"`
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
	dest, _ := destination.For(to.DestinationKind)
	m.Pickup = dest == destination.PickupPoint

	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode order.shipped: %w", err)
	}
	// Keyed on the CARRIER and the tracking number together, which is what
	// order_shipments is unique on: two carriers may legitimately issue the same
	// number, and the tracking number alone would then swallow the second
	// dispatch. An order in two parcels is still two notices.
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

	keys := make([]string, 0, len(claimed))
	payloads := make([][]byte, 0, len(claimed))
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
		keys = append(keys, c.ID.String())
		payloads = append(payloads, payload)
	}
	// One statement, because this runs while the variant row is locked and every
	// checkout of it waits for the loop to end.
	return outbox.Enqueue(ctx, q, outbox.TopicRestocked, 0, keys, payloads)
}
