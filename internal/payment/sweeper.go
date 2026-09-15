package payment

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// RefundWebhookSweepInterval is how often ignored refund events are replayed.
const RefundWebhookSweepInterval = 15 * time.Minute

// RefundWebhookBackfillLimit bounds one sweep so a large backlog cannot stall
// the worker loop.
const RefundWebhookBackfillLimit = 20

// SweepIgnoredRefundWebhooks replays stored refund webhooks that predate
// reconciliation support.
func (s *Store) SweepIgnoredRefundWebhooks(ctx context.Context, log *slog.Logger) (int, error) {
	n, err := s.BackfillIgnoredRefundWebhooks(ctx, RefundWebhookBackfillLimit)
	if err != nil && !errors.Is(err, context.Canceled) {
		return 0, err
	}
	if n > 0 {
		log.InfoContext(ctx, "reconciled ignored refund webhooks", "applied", n)
	}
	return n, err
}

// SweepIgnoredRefundWebhooksForever runs the bounded backfill on a timer.
func (s *Store) SweepIgnoredRefundWebhooksForever(ctx context.Context, log *slog.Logger) {
	t := time.NewTicker(RefundWebhookSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.SweepIgnoredRefundWebhooks(ctx, log); err != nil && !errors.Is(err, context.Canceled) {
				log.ErrorContext(ctx, "sweep ignored refund webhooks", "error", err)
			}
		}
	}
}
