//go:build integration

package orders

import (
	"context"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/outbox"
)

// EnqueueShippedNotice exposes the dispatch-notice producer only to integration
// fixtures, which cannot drive a whole fulfilment to reach it.
func (s *Store) EnqueueShippedNotice(ctx context.Context, orderID uuid.UUID, carrier, tracking string) error {
	return enqueueOrderShipped(ctx, s.q, orderID, &outbox.OrderShipped{Carrier: carrier, Tracking: tracking})
}

// FillParcel exposes the parcel writer to integration tests, which reach the
// schema's own refusal of a unit by naming one the dispatch form would not
// offer.
func (s *Store) FillParcel(ctx context.Context, orderID, shipmentID, lineID, reservationID uuid.UUID, quantity int32) error {
	return fillParcel(ctx, s.q, orderID, shipmentID, []parcelLine{{lineID: lineID, reservationID: reservationID, quantity: quantity}})
}
