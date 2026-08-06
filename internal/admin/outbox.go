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
type OrderShipped struct {
	// Locale must match email.OrderShipped. The two structs are
	// deliberately separate — a consumer that imports the producer's types
	// cannot be deployed a version behind — and TestEveryMailPayloadMatchesItsProducer
	// is what stops the copies drifting.
	Locale      string `json:"locale"`
	OrderNumber string `json:"order_number"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Carrier     string `json:"carrier"`
	Tracking    string `json:"tracking"`
}

// RestockNotice is what a catalogue.restocked message carries.
type RestockNotice struct {
	// Locale must match email.RestockNotice. The two structs are
	// deliberately separate — a consumer that imports the producer's types
	// cannot be deployed a version behind — and TestEveryMailPayloadMatchesItsProducer
	// is what stops the copies drifting.
	Locale      string `json:"locale"`
	Email       string `json:"email"`
	ProductName string `json:"product_name"`
	Slug        string `json:"slug"`
	SKU         string `json:"sku"`
}

// enqueueOrderShipped writes the dispatch notice in Ship's own transaction.
//
// Shipping is the moment the customer is waiting on, and until now nothing told
// them: the tracking number went into order_shipments and sat there for anybody
// who thought to look. The topic constant has existed the whole time with no
// producer.
func enqueueOrderShipped(ctx context.Context, q *db.Queries, orderID uuid.UUID, m *OrderShipped) error {
	to, err := q.ShipmentRecipient(ctx, orderID)
	if err != nil {
		return fmt.Errorf("read recipient of order %s: %w", m.OrderNumber, err)
	}
	if to.Email == "" {
		// An erased order — there is nobody to tell, and that is not a failure.
		return nil
	}
	// The CUSTOMER's language, off the order — not the staff member's, who is the
	// one holding the request that got here.
	m.Email, m.Name, m.Locale = to.Email, to.RecipientName, to.Locale

	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode order.shipped: %w", err)
	}
	// Keyed on the TRACKING number rather than the order: an order shipped in
	// two parcels is two notices, and each says which parcel it is about.
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
// The claim and the enqueue commit together, which is what makes notified_at
// honest: it means "the outbox has this", and the outbox is what guarantees
// delivery from there. Claimed without enqueuing, the customer is marked told
// and never hears anything — and the partial unique index means they cannot
// even ask again.
//
// A stock movement that does not cross the in-stock threshold claims nothing,
// because the UPDATE's own EXISTS checks it. That is why this can be called on
// every adjustment rather than only on the ones somebody decided were restocks.
func enqueueRestockNotices(ctx context.Context, q *db.Queries, variantID uuid.UUID) error {
	claimed, err := q.ClaimRestockNotices(ctx, variantID)
	if err != nil {
		return fmt.Errorf("claim restock notices: %w", err)
	}
	if len(claimed) == 0 {
		return nil
	}

	// One read per LOCALE, not per recipient: the product name has to be in each
	// reader's language, and there are two languages and can be dozens of
	// subscribers. The letter already followed the row's locale and the product name
	// did not, so an English subscriber got an English letter about 保護殼.
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
			// Recorded when they asked. A stock adjustment is a back-office
			// action, so the request here belongs to the shop and not to them.
			Locale: c.Locale,
		})
		if encErr != nil {
			return fmt.Errorf("encode restock notice: %w", encErr)
		}
		// Keyed on the notification row, which is unique and already spent by
		// the claim above — so a retried adjustment cannot produce a second
		// mail for the same request.
		if err := q.EnqueueMessage(ctx, db.EnqueueMessageParams{
			Topic: outbox.TopicRestocked, DedupeKey: c.ID.String(), Payload: payload,
		}); err != nil {
			return fmt.Errorf("enqueue restock notice: %w", err)
		}
	}
	return nil
}
