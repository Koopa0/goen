package cart

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotCancellable is an order the customer may no longer call off. One error
// for every reason, because telling them apart on a guessable order number
// would say which numbers are real and what state they are in.
var ErrNotCancellable = errors.New("cart: this order cannot be cancelled")

// Cancel calls off an order the customer has not paid for.
//
// It returns the Checkout Sessions the caller must close at Stripe once this has
// committed: holding a transaction open across a third-party call would put the
// stock release and the credit reversal at the mercy of Stripe's latency.
func (s *Store) Cancel(ctx context.Context, number string) ([]string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin cancel: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	held, err := q.HeldReservationsForOrder(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("read holds of %s: %w", number, err)
	}

	cancelled, err := q.CancelOrderByCustomer(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("cancel %s: %w", number, err)
	}
	if cancelled == 0 {
		return nil, ErrNotCancellable
	}

	// The stock comes back AFTER the status changes, and that order is what makes
	// it legal: release_reservation refuses a committed order's hold, and
	// cancelling is exactly what stops an order being committed.
	for _, id := range held {
		if relErr := q.ReleaseReservation(ctx, id); relErr != nil {
			// Not skipped the way the sweeper skips: this order is being cancelled
			// in this transaction, so a hold that cannot be released rolls it back.
			return nil, fmt.Errorf("release hold %s of %s: %w", id, number, relErr)
		}
	}

	// The money, also after the status change: store_credit_guard refuses a
	// reversal on an order that is neither a pending unpaid checkout nor
	// cancelled.
	order, err := q.OrderIDByNumber(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("read order %s: %w", number, err)
	}
	if _, err := q.ReverseOrderCredit(ctx, order.ID); err != nil {
		return nil, fmt.Errorf("return store credit spent on %s: %w", number, err)
	}

	if err := q.RecordCancellation(ctx, number); err != nil {
		return nil, fmt.Errorf("record cancellation of %s: %w", number, err)
	}

	// Read inside the transaction, so the set handed back is the one this
	// cancellation saw rather than whatever a webhook leaves behind afterwards.
	sessions, sessErr := q.OpenSessionsForOrder(ctx, number)
	if sessErr != nil {
		return nil, fmt.Errorf("read open checkout sessions of %s: %w", number, sessErr)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit cancel: %w", err)
	}
	return sessions, nil
}
