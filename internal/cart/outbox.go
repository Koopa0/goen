package cart

import (
	"context"
	"fmt"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/ui/pages"
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
	lines, err := q.OrderPageLines(ctx, db.OrderPageLinesParams{
		OrderID: summary.ID, Locale: i18n.FromContext(ctx).Tag(),
	})
	if err != nil {
		return fmt.Errorf("read lines for order.placed: %w", err)
	}
	hold, err := q.OrderHoldSpan(ctx, summary.ID)
	if err != nil {
		return fmt.Errorf("read stock hold for order.placed: %w", err)
	}
	// finishOrder persists this same delivery after the confirmation is queued.
	snapshot := &email.PlacedSnapshot{
		SubtotalCents: summary.SubtotalCents, ShippingCents: summary.ShippingCents,
		DiscountCents: summary.DiscountCents, DiscountReason: summary.DiscountReason, TaxCents: summary.TaxCents,
		CreditCents: summary.CreditCents, ShippingName: summary.ShippingMethodName,
		Phone: addr.Phone, DeliveryNote: addr.Note, HoldUntil: hold.HeldUntil, StartBy: payment.StartBy(hold.HeldUntil),
		DeliveryTo: pages.Delivery{
			PostalCode: addr.PostalCode, City: addr.City, District: addr.District,
			Street: addr.Street, PickupChain: addr.PickupChain,
			PickupStoreCode: addr.PickupStoreCode, PickupStoreName: addr.PickupStoreName,
		}.Line(),
		Lines: make([]email.PlacedLine, 0, len(lines)),
	}
	for i := range lines {
		line := &lines[i]
		snapshot.Lines = append(snapshot.Lines, email.PlacedLine{
			SKU: line.SKU, Name: line.ProductName, Label: line.VariantLabel.String,
			UnitCents: line.UnitPriceCents, Quantity: line.Quantity,
		})
	}
	owed := summary.OwedCents
	return outbox.Enqueue(ctx, q, outbox.TopicOrderPlaced, number, &email.OrderPlaced{
		OrderNumber: number, Email: addr.Email, Name: addr.RecipientName, TotalCents: totalCents,
		OwedCents: &owed, Snapshot: snapshot, Locale: i18n.FromContext(ctx).Tag(),
	})
}
