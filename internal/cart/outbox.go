package cart

import (
	"context"
	"fmt"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
)

// enqueueOrderPlaced writes the message in the order's own transaction, keyed on
// the order number so a retried checkout cannot send a second confirmation.
func enqueueOrderPlaced(ctx context.Context, q *db.Queries, number string, addr *Address, totalCents int64) error {
	// Credit is already posted in this transaction, so order_amount_owed is
	// the figure the payment page will show.
	summary, err := q.OrderSummaryByNumber(ctx, db.OrderSummaryByNumberParams{
		Number: number, Locale: i18n.FromContext(ctx).Tag(),
	})
	if err != nil {
		return fmt.Errorf("read owed for order.placed: %w", err)
	}
	owed := summary.OwedCents
	return outbox.Enqueue(ctx, q, outbox.TopicOrderPlaced, number, &email.OrderPlaced{
		OrderNumber: number, Email: addr.Email, Name: addr.Name, TotalCents: totalCents,
		OwedCents: &owed, Locale: i18n.FromContext(ctx).Tag(),
	})
}
