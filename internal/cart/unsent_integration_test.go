//go:build integration

package cart_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/returns"
)

// The customer's order page after the shop refunded what it could not send:
// the history says those units were not sent and were refunded, and they are
// no longer listed as still to come.
func TestTheOrderPageSaysWhatWasNotSentWasRefunded(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	o := admintest.PartlyShippedOrder(t, pool, admintest.TwoLineOrderTotal)
	s := cart.NewStore(admintest.StoreRolePool(t, pool))
	before, err := s.Order(i18n.WithLocale(ctx, i18n.ZhHant), o.Number)
	if err != nil {
		t.Fatalf("read the order page: %v", err)
	}
	if len(before.Unshipped) != 1 || before.Unshipped[0].Quantity != 2 {
		t.Fatalf("before the refund the page waits for %+v, want B × 2", before.Unshipped)
	}

	if refundErr := refunds.NewStore(admintest.AdminRolePool(t, pool), admintest.Refunder{}, nil).RefundUnsent(ctx, o.Number,
		&refunds.UnsentRequest{Units: map[uuid.UUID]int32{o.LineB: 2}, Reason: string(returns.UnsentLost), Note: "物流遺失"}); refundErr != nil {
		t.Fatalf("refund the unsent units: %v", refundErr)
	}
	view, err := s.Order(i18n.WithLocale(ctx, i18n.ZhHant), o.Number)
	if err != nil {
		t.Fatalf("read the order page: %v", err)
	}
	if len(view.Unshipped) != 0 {
		t.Errorf("the page still waits for %+v after they were refunded", view.Unshipped)
	}
	var labels []string
	for _, e := range view.Timeline {
		if e.Kind == order.EventRefunded {
			labels = append(labels, i18n.T(i18n.WithLocale(ctx, i18n.ZhHant), e.LabelKey()))
		}
	}
	if len(labels) != 1 || labels[0] != "無法出貨，已退款" {
		t.Errorf("the history's refund reads %v, want one 無法出貨，已退款", labels)
	}
}
