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
//
// It is short relative to HoldTTL on purpose: the cost of sweeping is one
// indexed query that usually finds nothing, and the cost of NOT sweeping is
// stock that a customer can see is "out of stock" while it sits against a
// checkout somebody closed half an hour ago.
const SweepInterval = time.Minute

// SweepBatch bounds one pass.
//
// A sweeper that tries to clear a year of abandoned checkouts in a single pass
// holds locks for as long as that takes, and every one of those locks is on a
// variant row that live checkouts need. It runs again in a minute; there is no
// reason for one pass to be large.
const SweepBatch = 200

// Sweep returns expired holds to the shelf, once.
//
// Each release is its OWN statement rather than one transaction over the batch.
// A reservation that cannot be released — its order was committed in the
// microsecond between the query and the write — must not take the other
// hundred-and-ninety-nine with it.
//
// It reports what it released and what it left alone. `skipped` is not
// decoration: "another instance got there first" and "the order became
// committed" are the sweeper working correctly, and an operator — or a test —
// that could not tell those from a real failure would have no way to know
// whether the benign-refusal path still works.
func (s *Store) Sweep(ctx context.Context, log *slog.Logger) (released, skipped int, err error) {
	ids, err := s.q.ExpiredReservations(ctx, SweepBatch)
	if err != nil {
		return 0, 0, err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			// Shutting down. What is already released stays released; the rest
			// is still expired and the next start will find it.
			return released, skipped, ctx.Err()
		}
		if relErr := s.q.ReleaseReservation(ctx, id); relErr != nil {
			// Two states are ordinary rather than exceptional: another instance
			// released it first, or the order became committed while this pass
			// was running. Both mean "not ours to release", and both are the
			// sweeper being safe rather than the sweeper being broken.
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

// benignSweepFailure reports whether a refusal is one the sweeper expects.
//
// Bound to the constraint NAME, not to the message: release_reservation raises
// with an explicit CONSTRAINT for exactly this, and matching on text would
// break the first time somebody rewords it.
func benignSweepFailure(err error) bool {
	return BenignSweepFailure(err)
}

// BenignSweepFailure reports whether a refusal is one the sweeper expects.
//
// Exported so a test can assert the classification directly. It is the only
// observable difference between "the sweeper is being safe" and "the sweeper is
// broken", and a package-private version left that difference untestable.
func BenignSweepFailure(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return false
	}
	switch pgErr.ConstraintName {
	case "inventory_reservation_state",
		"inventory_reservation_committed_no_release",
		// A zero-owed order is paid for and still pending, so committed_orders
		// reports it false. ExpiredReservations already declines to offer one,
		// which makes this the belt to that query's braces: the refusal is the
		// database's, and the sweeper must read it as being safe rather than
		// broken however the row was selected.
		"inventory_reservation_funded_no_release":
		return true
	default:
		return false
	}
}

// SweepForever runs Sweep on a ticker until ctx is cancelled.
//
// The caller owns the goroutine — this blocks, so main can wg.Go it and wait
// for it at shutdown. A sweeper started and forgotten is the fire-and-forget
// the concurrency rules refuse: nothing would be waiting for it, and a shutdown
// could cut a release between its two writes.
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

// AttemptRetain is how long a checkout idempotency key is kept.
//
// Thirty days. Nothing ever deleted these: one row per checkout attempt goen has
// ever seen, successful or abandoned, keyed on what the form carried.
//
// The window is weeks rather than hours because deleting a row frees its key to
// be replayed. A key belongs to one rendered form — a browser holding one for a
// month has been closed — and a double-submit that far apart is not the failure
// this table defends against.
const AttemptRetain = 30 * 24 * time.Hour

// AttemptSweepInterval is how often that runs.
//
// Its own worker and its own ticker, rather than a third statement inside
// [Store.Sweep]. That runs every minute, because a hold expiring is stock a
// customer is waiting for; a retention delete on a growing table every sixty
// seconds is a cost with no reader. Daily is what retention measured in weeks
// asks for.
const AttemptSweepInterval = 24 * time.Hour

// GrantRetain is how long a browser's proof of access to an order is kept.
//
// It matches the placed-order cookie's own MaxAge, because that is what decides
// when a grant stops being reachable: the token exists in exactly one place, the
// browser, and when that cookie expires the row behind it can never be presented
// again. Keeping it is keeping a live bearer credential for nobody — one lifted
// from a proxy log or an old backup opens the order page, the cancel form and
// the return form, and did so indefinitely.
//
// Equal to [cookieMaxAge] rather than derived from it in the same expression,
// because they answer different questions and a future decision to lengthen one
// is not automatically a decision about the other. If they diverge, this is the
// one that must be the LONGER: a grant swept while its cookie is still live
// locks a customer out of their own order.
//
// Equality alone did not deliver that, and the sentence above was false for as
// long as it existed. The cookie is RE-ISSUED with a fresh MaxAge on every order
// and carries up to ten older tokens forward, while a grant was swept on its own
// created_at — so a customer who ordered on day 0 and again on day 25 held a live
// cookie until day 55 naming an order whose grant died on day 30. Two intervals
// are only comparable if they start from the same event, which is why
// TouchOrderAccessGrants restarts this one wherever the cookie's is restarted.
const GrantRetain = cookieMaxAge * time.Second

// SweepAttempts deletes checkout keys past [AttemptRetain] and access grants
// past [GrantRetain], once.
//
// Two statements on one worker rather than two workers, because they are the
// same idea on the same schedule: a row whose only reader has gone. Both are
// retention measured in weeks, and neither is stock somebody is waiting for.
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
