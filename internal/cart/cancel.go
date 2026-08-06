package cart

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotCancellable is an order the customer may no longer call off.
//
// One error for every reason — already shipped, already paid, already
// cancelled, does not exist — because the page's answer is the same in each
// case: this is not yours to cancel, talk to us. Telling them apart on an order
// number that is guessable would say which numbers are real and what state they
// are in.
var ErrNotCancellable = errors.New("cart: this order cannot be cancelled")

// Cancel calls off an order the customer has not paid for.
//
// Not committed is the whole rule, and committed_orders carries both halves of
// it: an order the shop has not started, that nobody has paid for. A paid order
// is not cancelled but refunded, and that decision belongs to the shop.
//
// The guard is in the UPDATE's own WHERE clause, where a capture racing the
// cancellation cannot slip between a read and a write.
//
// The status change, the stock release and the history entry are ONE
// transaction. Cancelling without releasing leaves the shelf short until the
// sweeper notices half an hour later — which is what the back office's cancel
// did, and the reason this function exists rather than a call to it.
//
// The Checkout Sessions it returns are the ones the CALLER must close at Stripe
// once this has committed. They are deliberately not closed here: expiring a
// session is a network call to a third party, and holding a transaction open
// across one would put the stock release and the credit reversal at the mercy of
// Stripe's latency. Post-commit is also the only ordering that is honest — a
// session closed for a cancellation that then rolled back would have taken away
// a checkout the customer is still entitled to finish.
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

	// The stock comes back AFTER the status changes, and that order is what
	// makes it legal: release_reservation refuses a COMMITTED order's hold, and
	// cancelling is exactly what stops an order being committed. Released
	// first, the same call refuses a funded order the back office is allowed to
	// cancel — which is how the two views came to be separated.
	for _, id := range held {
		if relErr := q.ReleaseReservation(ctx, id); relErr != nil {
			// NOT skipped the way the sweeper skips. The sweeper walks holds it
			// did not choose and benign refusals are ordinary there; here the
			// order is being cancelled in this transaction, so a hold that
			// cannot be released means the cancellation is wrong and the whole
			// thing rolls back.
			return nil, fmt.Errorf("release hold %s of %s: %w", id, number, relErr)
		}
	}

	// And the money. store_credit_entries.reverses_id, its unique index and the
	// whole reversal branch of store_credit_guard were written with the ledger and
	// had NO CALLER: a cancellation released the stock and left the credit spent,
	// so the goods went back on the shelf and the customer's money did not go back
	// to them.
	//
	// AFTER the status change, exactly like the release above and for the same
	// reason — store_credit_guard refuses a reversal on an order that is neither a
	// pending unpaid checkout nor cancelled, and cancelling is what makes it legal.
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

	// Read last and inside the transaction, so the set handed back is the one
	// this cancellation saw. A read after the commit would race a webhook that
	// captures or expires a session in between, and hand the caller a session id
	// whose row has already moved on.
	sessions, sessErr := q.OpenSessionsForOrder(ctx, number)
	if sessErr != nil {
		return nil, fmt.Errorf("read open checkout sessions of %s: %w", number, sessErr)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit cancel: %w", err)
	}
	return sessions, nil
}
