package cart

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/db"
)

const GuestCartSweepInterval = 24 * time.Hour

// GuestCartSweepBatch bounds one delete, because each cart takes its lines with
// it and the first pass after a deploy may find a long backlog.
const GuestCartSweepBatch = 500

// SweepGuestCarts deletes guest carts idle for [GuestCartRetain], a batch at a
// time until a batch comes back short, and reports how many went. An account's
// cart is never touched.
func (s *Store) SweepGuestCarts(ctx context.Context) (int64, error) {
	retain := pgtype.Interval{Microseconds: int64(GuestCartRetain / time.Microsecond), Valid: true}
	var total int64
	for {
		n, err := s.q.DeleteIdleGuestCarts(ctx, db.DeleteIdleGuestCartsParams{
			Retain: retain, Batch: GuestCartSweepBatch,
		})
		total += n
		if err != nil {
			return total, fmt.Errorf("delete idle guest carts: %w", err)
		}
		if n < GuestCartSweepBatch {
			return total, nil
		}
	}
}

// SweepGuestCartsForever runs once at startup, so a host redeployed more often
// than the interval still clears idle guest carts.
func (s *Store) SweepGuestCartsForever(ctx context.Context, log *slog.Logger) {
	t := time.NewTicker(GuestCartSweepInterval)
	defer t.Stop()
	for {
		n, err := s.SweepGuestCarts(ctx)
		if err != nil && ctx.Err() == nil {
			log.ErrorContext(ctx, "sweep idle guest carts", "error", err)
		}
		if n > 0 {
			log.InfoContext(ctx, "deleted idle guest carts", "deleted", n,
				"retain_days", int(GuestCartRetain.Hours()/24))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
