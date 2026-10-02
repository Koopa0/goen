package cart

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/email"
)

const SweepInterval = time.Minute

// SweepBatch bounds one pass, because every release holds a lock on a variant
// row that live checkouts need.
const SweepBatch = 200

// Sweep releases and cancels each in its own transaction, so one failure does
// not take the batch with it.
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
	return released, skipped, s.cancelLapsed(ctx, log)
}

// cancelLapsed returns the coupon slot and store credit spent on the order.
func (s *Store) cancelLapsed(ctx context.Context, log *slog.Logger) error {
	numbers, err := s.q.LapsedUnpaidOrders(ctx, SweepBatch)
	if err != nil {
		return fmt.Errorf("read lapsed unpaid orders: %w", err)
	}
	for _, number := range numbers {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		cancelled, cancelErr := s.cancelLapsedOrder(ctx, number)
		if cancelErr != nil {
			log.ErrorContext(ctx, "cancel an order whose payment window closed",
				"order", number, "error", cancelErr)
			continue
		}
		if cancelled {
			log.InfoContext(ctx, "cancelled an order whose payment window closed", "order", number)
		}
	}
	return nil
}

// cancelLapsedOrder reports false when the order stopped qualifying between the
// candidate read and its lock: paid, cancelled, or a payment opened.
func (s *Store) cancelLapsedOrder(ctx context.Context, number string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin cancel of %s: %w", number, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	orderID, err := q.LockOrderByNumber(ctx, number)
	if err != nil {
		return false, fmt.Errorf("lock order %s: %w", number, err)
	}
	cancelled, err := q.CancelLapsedOrder(ctx, orderID)
	if err != nil {
		return false, fmt.Errorf("cancel %s: %w", number, err)
	}
	if cancelled == 0 {
		return false, nil
	}
	// No Checkout Session is left to close: the predicate refused any order
	// with a payment that could still take money.
	if err := settleCancellation(ctx, q, number, email.TerminalCancelledByPaymentDeadline); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit cancel of %s: %w", number, err)
	}
	return true, nil
}

func benignSweepFailure(err error) bool {
	return BenignSweepFailure(err)
}

// BenignSweepFailure is bound to the constraint name, because a PgError's
// message never carries it.
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
		// ExpiredReservations normally filters this state; the named refusal is
		// the order-lock recheck when reconciliation begins after that
		// snapshot.
		"inventory_reservation_payment_reconciliation_no_release":
		return true
	default:
		return false
	}
}

// SweepForever blocks; the caller owns the goroutine.
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

// AttemptRetain is weeks rather than hours, because deleting a row frees its
// key to be replayed.
const AttemptRetain = 30 * 24 * time.Hour

const AttemptSweepInterval = 24 * time.Hour

// GrantRetain must never be SHORTER than the placed cookie's MaxAge, which
// holds only because TouchOrderAccessGrants restarts this clock with the
// cookie's.
const GrantRetain = cookieMaxAge * time.Second

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
