package cart

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotCancellable is an order the customer may no longer call off. One error
// for every reason, so a guessable order number cannot be probed for its state.
var ErrNotCancellable = errors.New("cart: this order cannot be cancelled")

// Cancel calls off an order the customer has not paid for, and returns the
// Checkout Sessions the caller must close at Stripe once this has committed.
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

	// Stock and credit come back AFTER the status change: release_reservation and
	// store_credit_guard both refuse while the order is still a live checkout.
	for _, id := range held {
		if relErr := q.ReleaseReservation(ctx, id); relErr != nil {
			return nil, fmt.Errorf("release hold %s of %s: %w", id, number, relErr)
		}
	}

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

	sessions, sessErr := q.OpenSessionsForOrder(ctx, number)
	if sessErr != nil {
		return nil, fmt.Errorf("read open checkout sessions of %s: %w", number, sessErr)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit cancel: %w", err)
	}
	return sessions, nil
}
