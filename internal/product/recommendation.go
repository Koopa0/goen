package product

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/web"
)

type recommendationRead string

const (
	readRelatedProducts recommendationRead = "related_products"
	readBoughtTogether  recommendationRead = "bought_together"
)

type recommendationFailure string

const (
	recommendationQueryFailed recommendationFailure = "query_failed"
	recommendationTimedOut    recommendationFailure = "timed_out"
)

const recommendationReadBudget = 150 * time.Millisecond

func recommendationContext(parent context.Context) (context.Context, context.CancelFunc) {
	budget := recommendationReadBudget
	if deadline, ok := parent.Deadline(); ok {
		// Two optional reads must leave time for the remaining core page reads.
		budget = min(budget, time.Until(deadline)/4)
	}
	return context.WithTimeout(parent, budget)
}

func (s *Store) recommendationError(ctx context.Context, operation recommendationRead, productID uuid.UUID, err error) error {
	if parentErr := ctx.Err(); parentErr != nil {
		return parentErr
	}
	reason := recommendationQueryFailed
	if errors.Is(err, context.DeadlineExceeded) {
		reason = recommendationTimedOut
	}
	attrs := []slog.Attr{
		slog.String("operation", string(operation)),
		slog.String("product_id", productID.String()),
		slog.String("reason", string(reason)),
	}
	if requestID := web.RequestID(ctx); requestID != "" {
		attrs = append(attrs, slog.String("request_id", requestID))
	}
	s.logger.LogAttrs(ctx, slog.LevelWarn, "product recommendations unavailable", attrs...)
	return nil
}
