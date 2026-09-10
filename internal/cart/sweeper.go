package cart

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// SweepInterval is how often abandoned holds are returned to the shelf.
const SweepInterval = time.Minute

// SweepBatch bounds one pass, because every release holds a lock on a variant
// row that live checkouts need.
const SweepBatch = 200

// Sweep returns expired holds to the shelf, once. Each release is its own
// statement, so one that cannot be released does not take the batch with it.
func (s *Store) Sweep(ctx context.Context, log *slog.Logger) (released, skipped int, err error) {
	ids, err := s.q.ExpiredReservations(ctx, SweepBatch)
	if err != nil {
		return 0, 0, err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return released, skipped, ctx.Err()
		}
		if relErr := s.q.ReleaseReservation(ctx, id); relErr != nil {
			if benignSweepFailure(relErr) {
				skipped++
				continue
			}
			log.ErrorContext(ctx, "release expired reservation", "reservation", id, "error", relErr)
			continue
		}
		released++
	}
	return released, skipped, nil
}

func benignSweepFailure(err error) bool {
	return BenignSweepFailure(err)
}

// BenignSweepFailure reports whether a refusal is one the sweeper expects.
// Bound to the constraint name, because a PgError's message never carries it.
func BenignSweepFailure(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return false
	}
	switch pgErr.ConstraintName {
	case "inventory_reservation_state",
		"inventory_reservation_committed_no_release",
		// A zero-owed order is paid for and still pending, so committed_orders
		// reports it false while release_reservation refuses it by name.
		"inventory_reservation_funded_no_release",
		// ExpiredReservations normally filters this state. The named refusal is
		// the order-lock recheck when reconciliation begins after that snapshot.
		"inventory_reservation_payment_reconciliation_no_release":
		return true
	default:
		return false
	}
}

// SweepForever runs Sweep on a ticker until ctx is cancelled. It blocks, so the
// caller owns the goroutine.
func (s *Store) SweepForever(ctx context.Context, log *slog.Logger) {
	t := time.NewTicker(SweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, skipped, err := s.Sweep(ctx, log)
			if err != nil && !errors.Is(err, context.Canceled) {
				log.ErrorContext(ctx, "sweep expired reservations", "error", err)
				continue
			}
			if n > 0 || skipped > 0 {
				log.InfoContext(ctx, "returned expired holds to the shelf",
					"released", n, "skipped", skipped)
			}
		}
	}
}

// AttemptRetain is how long a checkout idempotency key is kept. Weeks rather
// than hours, because deleting a row frees its key to be replayed.
const AttemptRetain = 30 * 24 * time.Hour

// AttemptSweepInterval is how often that runs.
const AttemptSweepInterval = 24 * time.Hour

// GrantRetain is how long a browser's proof of access to an order is kept. It
// must never be SHORTER than the placed cookie's MaxAge, which holds only
// because TouchOrderAccessGrants restarts this clock with the cookie's.
const GrantRetain = cookieMaxAge * time.Second

// SweepAttempts deletes checkout keys past [AttemptRetain] and access grants
// past [GrantRetain], once.
func (s *Store) SweepAttempts(ctx context.Context) error {
	if err := s.q.DeleteOldCheckoutAttempts(ctx, pgtype.Interval{
		Microseconds: int64(AttemptRetain / time.Microsecond), Valid: true,
	}); err != nil {
		return fmt.Errorf("delete old checkout attempts: %w", err)
	}
	if err := s.q.DeleteOldOrderAccessGrants(ctx, pgtype.Interval{
		Microseconds: int64(GrantRetain / time.Microsecond), Valid: true,
	}); err != nil {
		return fmt.Errorf("delete old order access grants: %w", err)
	}
	return nil
}

// SweepAttemptsForever runs SweepAttempts on a ticker until ctx is cancelled.
func (s *Store) SweepAttemptsForever(ctx context.Context, log *slog.Logger) {
	t := time.NewTicker(AttemptSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.SweepAttempts(ctx); err != nil && ctx.Err() == nil {
				log.ErrorContext(ctx, "sweep old checkout attempts", "error", err)
			}
		}
	}
}
