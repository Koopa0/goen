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

// CancelOrder refuses an order with an unresolved payment, so no Checkout
// Session is left for the caller to close.
func (s *Store) CancelOrder(ctx context.Context, number string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin cancel: %w", err)
	}
	defer pgtx.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	// The lock is taken before cancellation or the expiry sweeper can touch the
	// live holds, and before the payment test, which must see every capture
	// committed ahead of it.
	orderID, err := q.LockOrderByNumber(ctx, number)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotCancellable
	}
	if err != nil {
		return fmt.Errorf("lock %s: %w", number, err)
	}
	cancelled, err := q.CancelOrderByCustomer(ctx, orderID)
	if err != nil {
		return fmt.Errorf("cancel %s: %w", number, err)
	}
	if cancelled == 0 {
		return ErrNotCancellable
	}
	if _, err := ordercancel.Settle(ctx, q, &ordercancel.Order{
		ID: orderID, Number: number, Kind: email.TerminalCancelledByCustomer, VoidTrigger: "cancel:" + number,
	}); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit cancel: %w", err)
	}
	return nil
}
