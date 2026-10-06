// Package ordercancel holds what cancelling an order that was never committed
// does besides moving its status.
package ordercancel

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/ordernotice"
	"github.com/koopa0/goen/internal/outbox"
)

// Order is the cancelled order and who cancelled it. Actor is invalid for the
// customer's own cancellation and the sweeper's. VoidTrigger names the request
// that owes the 統一發票 void of an order store credit alone paid.
type Order struct {
	ID          uuid.UUID
	Number      string
	Actor       uuid.NullUUID
	Kind        email.TerminalKind
	VoidTrigger string
}

// Settle must run in the transaction whose status UPDATE holds the order lock.
// It returns the store credit given back.
func Settle(ctx context.Context, q *db.Queries, o *Order) (creditReturnedCents int64, err error) {
	// Read before the credit is reversed: owing nothing after a spend is what
	// says credit paid the order, and checkout queued its invoice then.
	paidByCredit, err := q.PaidByCreditAlone(ctx, o.ID)
	if err != nil {
		return 0, fmt.Errorf("read how %s was paid: %w", o.Number, err)
	}

	held, err := q.HeldReservationsForOrder(ctx, o.Number)
	if err != nil {
		return 0, fmt.Errorf("read holds of %s: %w", o.Number, err)
	}
	// Stock and credit come back AFTER the status change: release_reservation and
	// store_credit_guard both refuse while the order is still a live checkout.
	for _, id := range held {
		if relErr := q.ReleaseReservation(ctx, id); relErr != nil {
			return 0, fmt.Errorf("release hold %s of %s: %w", id, o.Number, relErr)
		}
	}
	creditReturnedCents, err = q.ReverseOrderCredit(ctx, o.ID)
	if err != nil {
		return 0, fmt.Errorf("return store credit spent on %s: %w", o.Number, err)
	}

	err = q.RecordCancellation(ctx, db.RecordCancellationParams{
		OrderID: o.ID, ActorUserID: o.Actor, BySystem: o.Kind == email.TerminalCancelledByPaymentDeadline,
	})
	if err != nil {
		return 0, fmt.Errorf("record cancellation of %s: %w", o.Number, err)
	}

	facts, err := q.OrderPaymentFacts(ctx, o.ID)
	if err != nil {
		return 0, fmt.Errorf("read payments of %s: %w", o.Number, err)
	}
	err = ordernotice.Enqueue(ctx, q, &email.OrderTerminal{
		OrderID: o.ID, Kind: o.Kind, Refunded: mayHaveTakenMoney(facts.Statuses, facts.ProviderFlagged),
	})
	if err != nil {
		return 0, err
	}

	if paidByCredit {
		err = invoice.EnqueueVoidDue(ctx, q, &outbox.InvoiceVoidDue{OrderNumber: o.Number, Trigger: o.VoidTrigger})
		if err != nil {
			return 0, err
		}
	}
	return creditReturnedCents, nil
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
