package buyer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/order/cancellation"
	"github.com/koopa0/goen/internal/outbox"
)

// ErrNotCancellable covers every reason, so a guessable order number cannot be
// probed for its state. Handler.CancelOrder answers only a browser that holds
// the order, which is why CheckoutOpenError may say more.
var ErrNotCancellable = errors.New("buyer: this order cannot be cancelled")

// CheckoutOpenError is ErrNotCancellable for an order that nothing else holds
// back: a Checkout Session of it is still open at Stripe and could yet be paid,
// and no payment has begun. HoldUntil is when the order's stock hold runs out,
// and the sweeper then cancels it unpaid; it is zero without one.
type CheckoutOpenError struct {
	HoldUntil time.Time
}

func (e *CheckoutOpenError) Error() string {
	return "buyer: a checkout session of this order may still be paid"
}

func (e *CheckoutOpenError) Unwrap() error { return ErrNotCancellable }

// CancelOrder refuses an order with an unresolved payment, so no Checkout
// Session is left for the caller to close. Where the Checkout Session is the only
// obstacle it says so, and the caller may expire the session and ask again.
func (s *Store) CancelOrder(ctx context.Context, number string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin cancel: %w", err)
	}
	defer db.Rollback(ctx, tx)
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
		return s.refusal(ctx, q, orderID)
	}
	if _, err := cancellation.Settle(ctx, q, &cancellation.Order{
		ID: orderID, Number: number, Kind: outbox.TerminalCancelledByCustomer, VoidTrigger: "cancel:" + number,
	}); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit cancel: %w", err)
	}
	return nil
}

// refusal is ErrNotCancellable, or CheckoutOpenError when an open Checkout
// Session is all that stopped the cancellation. Under READ COMMITTED it reads a
// fresh snapshot, and writers that do not take the order lock can commit between
// the UPDATE and this read, so it may differ from what the UPDATE judged. Either
// way is safe: a stale "true" only leads to an expiry and a second CancelOrder,
// which judges again.
func (s *Store) refusal(ctx context.Context, q *db.Queries, orderID uuid.UUID) error {
	onlyCheckout, err := q.OnlyCheckoutBlocksCancel(ctx, orderID)
	if err != nil {
		return fmt.Errorf("read what blocks the cancel of order %s: %w", orderID, err)
	}
	if !onlyCheckout {
		return ErrNotCancellable
	}
	span, err := q.OrderHoldSpan(ctx, orderID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read the stock hold of order %s: %w", orderID, err)
	}
	return &CheckoutOpenError{HoldUntil: span.HeldUntil}
}
