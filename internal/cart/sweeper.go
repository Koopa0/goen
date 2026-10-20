package cart

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/db"
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
