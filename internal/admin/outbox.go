package admin

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/destination"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/outbox"
)

func enqueueOrderShipped(ctx context.Context, q *db.Queries, orderID uuid.UUID, m *email.OrderShipped) error {
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

	// Keyed on the CARRIER and the tracking number together, which is what
	// order_shipments is unique on: two carriers may legitimately issue the same
	// number, and the tracking number alone would then swallow the second
	// dispatch. An order in two parcels is still two notices.
	return outbox.Enqueue(ctx, q, outbox.TopicOrderShipped, m.Carrier+":"+m.Tracking, m)
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
	payloads := make([]email.RestockNotice, 0, len(claimed))
	for _, c := range claimed {
		subject := subjects[c.Locale]
		keys = append(keys, c.ID.String())
		payloads = append(payloads, email.RestockNotice{
			Email: c.Email, ProductName: subject.ProductName,
			Slug: subject.Slug, SKU: subject.SKU,
			Locale: c.Locale,
		})
	}
	// One statement, because this runs while the variant row is locked and every
	// checkout of it waits for the loop to end.
	return outbox.EnqueueAll(ctx, q, outbox.TopicRestocked, 0, keys, payloads)
}
