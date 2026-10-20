// Package lapse is an unpaid order's stock hold running out; it holds the
// sweeper that releases expired holds of uncommitted orders and cancels the
// orders whose last hold lapsed unpaid; it does not hold what a cancellation
// settles (order/cancellation) or the checkout sessions of a lapsed order
// (payment).
package lapse

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/order/cancellation"
	"github.com/koopa0/goen/internal/outbox"
)

// Sweeper holds the pool rather than a DBTX because each cancellation is its
// own transaction.
type Sweeper struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewSweeper(pool *pgxpool.Pool) *Sweeper {
	if pool == nil {
		panic("lapse: NewSweeper requires a pool")
	}
	return &Sweeper{pool: pool, q: db.New(pool)}
}

const SweepInterval = time.Minute

// SweepBatch bounds one pass, because every release holds a lock on a variant
// row that live checkouts need.
const SweepBatch = 200

// Sweep releases and cancels each in its own transaction, so one failure does
// not take the batch with it.
func (s *Sweeper) Sweep(ctx context.Context, log *slog.Logger) (released, skipped int, err error) {
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
func (s *Sweeper) cancelLapsed(ctx context.Context, log *slog.Logger) error {
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
func (s *Sweeper) cancelLapsedOrder(ctx context.Context, number string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin cancel of %s: %w", number, err)
	}
	defer db.Rollback(ctx, tx)
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
	if _, err := cancellation.Settle(ctx, q, &cancellation.Order{
		ID: orderID, Number: number, Kind: outbox.TerminalCancelledByPaymentDeadline, VoidTrigger: "cancel:" + number,
	}); err != nil {
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
func (s *Sweeper) SweepForever(ctx context.Context, log *slog.Logger) {
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
