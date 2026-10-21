package checkout

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// AttemptRetain is weeks rather than hours, because deleting a row frees its
// key to be replayed.
const AttemptRetain = 30 * 24 * time.Hour

const AttemptSweepInterval = 24 * time.Hour

func (s *Store) SweepAttempts(ctx context.Context) error {
	if err := s.q.DeleteOldCheckoutAttempts(ctx, pgtype.Interval{
		Microseconds: int64(AttemptRetain / time.Microsecond), Valid: true,
	}); err != nil {
		return fmt.Errorf("delete old checkout attempts: %w", err)
	}
	return nil
}

// SweepAttemptsForever runs once at startup, so a host redeployed more often
// than the interval still clears old attempts.
func (s *Store) SweepAttemptsForever(ctx context.Context, log *slog.Logger) {
	t := time.NewTicker(AttemptSweepInterval)
	defer t.Stop()
	for {
		if err := s.SweepAttempts(ctx); err != nil && ctx.Err() == nil {
			log.ErrorContext(ctx, "sweep old checkout attempts", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// DraftSweepInterval is a fraction of [DraftTTL] so a stale draft is gone
// within minutes: a guest's cart is never deleted, so nothing else would clear
// what a guest typed.
const DraftSweepInterval = DraftTTL / 4

func (s *Store) SweepDrafts(ctx context.Context) error {
	if err := s.q.ClearStaleCheckoutDrafts(ctx, pgtype.Interval{
		Microseconds: int64(DraftTTL / time.Microsecond), Valid: true,
	}); err != nil {
		return fmt.Errorf("clear stale checkout drafts: %w", err)
	}
	return nil
}

// SweepDraftsForever runs once at startup, so a host redeployed more often than
// the interval still clears drafts.
func (s *Store) SweepDraftsForever(ctx context.Context, log *slog.Logger) {
	t := time.NewTicker(DraftSweepInterval)
	defer t.Stop()
	for {
		if err := s.SweepDrafts(ctx); err != nil && ctx.Err() == nil {
			log.ErrorContext(ctx, "sweep stale checkout drafts", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
