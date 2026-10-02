//go:build integration

package admin

import (
	"context"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/email"
)

// EnqueueShippedNotice exposes the dispatch-notice producer only to integration
// fixtures, which cannot drive a whole fulfilment to reach it.
func (s *Store) EnqueueShippedNotice(ctx context.Context, orderID uuid.UUID, carrier, tracking string) error {
	return enqueueOrderShipped(ctx, s.q, orderID, &email.OrderShipped{Carrier: carrier, Tracking: tracking})
}
