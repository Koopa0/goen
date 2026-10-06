package cart

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/ordercancel"
	"github.com/koopa0/goen/internal/pgtx"
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
	defer pgtx.Rollback(ctx, tx)
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
	if _, err := ordercancel.Settle(ctx, q, &ordercancel.Order{
		ID: orderID, Number: number, Kind: email.TerminalCancelledByCustomer, VoidTrigger: "cancel:" + number,
	}); err != nil {
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
