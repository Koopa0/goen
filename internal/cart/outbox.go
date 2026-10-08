package cart

import (
	"context"
	"fmt"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/outbox"
)

// enqueueOrderPlaced is keyed on the order number so a retried checkout cannot
// send a second confirmation.
func enqueueOrderPlaced(ctx context.Context, q *db.Queries, number string, addr *order.Delivery, totalCents int64) error {
	// Credit is already posted in this transaction, so order_amount_after_credit is the
	// figure the payment page will show.
	summary, err := q.OrderSummaryByNumber(ctx, db.OrderSummaryByNumberParams{
		Number: number, Locale: i18n.FromContext(ctx).Tag(),
	})
	if err != nil {
		return fmt.Errorf("read owed for order.placed: %w", err)
	}
	owed := summary.OwedCents
	return outbox.Enqueue(ctx, q, outbox.TopicOrderPlaced, number, &email.OrderPlaced{
		OrderNumber: number, Email: addr.Email, Name: addr.RecipientName, TotalCents: totalCents,
		OwedCents: &owed, Snapshot: nil, Locale: i18n.FromContext(ctx).Tag(),
	})
}
