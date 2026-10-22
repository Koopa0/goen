//go:build integration

package buyer_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/order/returns"
)

// TestStore_ReturnOrderUnsentRefund: after the shop refunds units it could not
// send, ReturnOrder still lists the shipped units as returnable and lists no
// request the customer did not make.
//
// The units the shop refunded because it could not send them never reached the
// customer: they do not use up what the customer may return of what did arrive,
// and the refund is not a request the customer made.
func TestStore_ReturnOrderUnsentRefund(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	o := admintest.PartlyShippedOrder(t, pool, admintest.TwoLineOrderTotal)
	if err := refunds.NewStore(admintest.AdminRolePool(t, pool), admintest.Refunder{}, nil).RefundUnsent(ctx, o.Number,
		&refunds.UnsentRequest{Units: map[uuid.UUID]int32{o.LineB: 2}, Reason: string(returns.UnsentDamaged), Note: "外盒破損"}); err != nil {
		t.Fatalf("refund the unsent units: %v", err)
	}
	got, err := buyer.NewStore(admintest.StoreRolePool(t, pool)).ReturnOrder(ctx, o.Number)
	if err != nil {
		t.Fatalf("read the return form: %v", err)
	}
	returnable := map[string]int32{}
	for _, l := range got.Lines {
		returnable[l.ID] = l.Returnable
	}
	if returnable[o.LineA.String()] != 1 || returnable[o.LineB.String()] != 1 {
		t.Errorf("returnable A/B = %d/%d, want the one of each that arrived",
			returnable[o.LineA.String()], returnable[o.LineB.String()])
	}
	if len(got.Existing) != 0 {
		t.Errorf("the customer's page lists %+v, want no request: the shop refunded what it could not send", got.Existing)
	}
}

// TestStore_OrderUnsentRefund: after the shop refunds units it could not send,
// Order no longer lists them as still to come and its history labels the refund
// as not sent.
//
// The history says those units were not sent and were refunded, and they are no
// longer listed as still to come.
func TestStore_OrderUnsentRefund(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	o := admintest.PartlyShippedOrder(t, pool, admintest.TwoLineOrderTotal)
	s := buyer.NewStore(admintest.StoreRolePool(t, pool))
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
