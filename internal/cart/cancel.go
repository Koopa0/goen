package cart

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/ordernotice"
)

// ErrNotCancellable covers every reason, so a guessable order number cannot be
// probed for its state.
var ErrNotCancellable = errors.New("cart: this order cannot be cancelled")

// CancelOrder returns the Checkout Sessions the caller must close at Stripe
// once this has committed.
func (s *Store) CancelOrder(ctx context.Context, number string) ([]string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin cancel: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	// The lock is taken before cancellation or the expiry sweeper can touch the
	// live holds, and before the payment test, which must see every capture
	// committed ahead of it.
	orderID, err := q.LockOrderByNumber(ctx, number)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotCancellable
	}
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", number, err)
	}
	cancelled, err := q.CancelOrderByCustomer(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("cancel %s: %w", number, err)
	}
	if cancelled == 0 {
		return nil, ErrNotCancellable
	}
	if err := settleCancellation(ctx, q, number, email.TerminalCancelledByCustomer); err != nil {
		return nil, err
	}

	sessions, sessErr := q.OpenSessionsForOrder(ctx, number)
	if sessErr != nil {
		return nil, fmt.Errorf("read open checkout sessions of %s: %w", number, sessErr)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit cancel: %w", err)
	}
	return sessions, nil
}

// settleCancellation must run in the transaction whose status UPDATE holds the
// order lock.
func settleCancellation(ctx context.Context, q *db.Queries, number string, kind email.TerminalKind) error {
	held, err := q.HeldReservationsForOrder(ctx, number)
	if err != nil {
		return fmt.Errorf("read holds of %s: %w", number, err)
	}

	// Stock and credit come back AFTER the status change: release_reservation and
	// store_credit_guard both refuse while the order is still a live checkout.
	for _, id := range held {
		if relErr := q.ReleaseReservation(ctx, id); relErr != nil {
			return fmt.Errorf("release hold %s of %s: %w", id, number, relErr)
		}
	}

	order, err := q.OrderIDByNumber(ctx, number)
	if err != nil {
		return fmt.Errorf("read order %s: %w", number, err)
	}
	if _, err := q.ReverseOrderCredit(ctx, order.ID); err != nil {
		return fmt.Errorf("return store credit spent on %s: %w", number, err)
	}

	if err := q.RecordCancellation(ctx, db.RecordCancellationParams{
		OrderNumber: number, BySystem: kind == email.TerminalCancelledByPaymentDeadline,
	}); err != nil {
		return fmt.Errorf("record cancellation of %s: %w", number, err)
	}

	facts, factsErr := q.OrderPaymentFacts(ctx, order.ID)
	if factsErr != nil {
		return fmt.Errorf("read payments of %s: %w", number, factsErr)
	}
	return ordernotice.Enqueue(ctx, q, &email.OrderTerminal{
		OrderID: order.ID, Kind: kind, Refunded: mayHaveTakenMoney(facts.Statuses, facts.ProviderFlagged),
	})
}

// paymentExpired is the one payments.status that proves a session took nothing:
// Stripe confirmed it expired, or it ended with no money.
const paymentExpired = "cancelled"

// mayHaveTakenMoney reports whether the notice must not say nothing was
// charged. Any status but an expired one may: a session still open can be
// completed in another tab before the cancellation closes it.
func mayHaveTakenMoney(statuses []string, providerFlagged bool) bool {
	if providerFlagged {
		return true
	}
	for _, s := range statuses {
		if s != paymentExpired {
			return true
		}
	}
	return false
}
