package product

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
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

const recommendationAcquireBudget = 5 * time.Second

// Pool reads hold their connection; transaction-bound reads keep their existing queries.
func (s *Store) recommendationQueries(ctx context.Context) (*db.Queries, context.Context, func(), error) {
	q := s.q
	release := func() {}
	if pool, ok := s.dbtx.(*pgxpool.Pool); ok {
		// Dialing a replacement connection must not consume the optional query budget.
		budget := recommendationAcquireBudget
		if deadline, ok := ctx.Deadline(); ok {
			budget = min(budget, time.Until(deadline)/4)
		}
		acquireCtx, cancel := context.WithTimeout(ctx, budget)
		conn, err := pool.Acquire(acquireCtx)
		cancel()
		if err != nil {
			return nil, nil, nil, err
		}
		q = db.New(conn)
		release = conn.Release
	}
	readCtx, cancel := recommendationContext(ctx)
	return q, readCtx, func() {
		cancel()
		release()
	}, nil
}

func recommendationContext(parent context.Context) (context.Context, context.CancelFunc) {
	budget := recommendationReadBudget
	if deadline, ok := parent.Deadline(); ok {
		// Two optional reads must leave time for the remaining core page reads.
		budget = min(budget, time.Until(deadline)/4)
	}
	return context.WithTimeout(parent, budget)
}

func (s *Store) omitFailedRecommendation(ctx context.Context, operation recommendationRead, productID uuid.UUID, err error) error {
	if parentErr := ctx.Err(); parentErr != nil {
		return fmt.Errorf("read %s of product %s: %w", operation, productID, parentErr)
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
	s.log.LogAttrs(ctx, slog.LevelWarn, "product recommendations unavailable", attrs...)
	return nil
}
