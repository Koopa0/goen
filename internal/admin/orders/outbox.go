package orders

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
