//go:build integration

package buyer_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// TestStore_OrderTimelineRefundID holds that the shopper's order timeline never shows
// the provider's refund id.
func TestStore_OrderTimelineRefundID(t *testing.T) {
	ctx := t.Context()
	vid := ordertest.FreshVariant(t, pool, "refundid-1")
	orderID := ordertest.HeldOrderFor(t, pool, uuid.NullUUID{}, vid, -time.Hour, true)
	ordertest.RefundedAndCancelled(t, pool, orderID)

	var number, staffNote string
	if err := pool.QueryRow(ctx, `
		SELECT o.order_number, e.note FROM orders o
		JOIN order_events e ON e.order_id = o.id AND e.kind = 'refunded'
		WHERE o.id = $1`, orderID).Scan(&number, &staffNote); err != nil || !strings.HasPrefix(staffNote, "re_") {
		t.Fatalf("the fixture must keep the refund id on the event: %q, %v", staffNote, err)
	}
	view, err := buyer.NewStore(pool).Order(ctx, number)
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	var sawRefund bool
	for _, e := range view.Timeline {
		if e.Kind == "refunded" {
			sawRefund = true
		}
		if strings.Contains(e.Note, "re_") {
			t.Errorf("%s event shows the provider refund id %q", e.Kind, e.Note)
		}
	}
	if !sawRefund {
		t.Error("the refunded event is missing from the shopper's timeline")
	}
}
