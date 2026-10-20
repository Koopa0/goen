//go:build integration

package dashboard_test

import (
	"testing"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/i18n"
)

func TestTheDashboardAndTheQueueTabsSplitPendingTheSameWay(t *testing.T) {
	ctx := t.Context()
	queue := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	desk := storeOver(pool, health.NewStore(pool))
	tab := func(v orders.View, status orders.QueueFilter) int64 {
		for _, tb := range v.Tabs {
			if tb.Value == status {
				return tb.Count
			}
		}
		t.Fatalf("no %q tab", status)
		return 0
	}
	before, err := queue.List(ctx, "", "")
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	dashBefore, err := desk.Dashboard(ctx)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}

	unpaid := admintest.PlaceUnpaidOrder(t, pool)
	funded, _, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, false)

	after, err := queue.List(ctx, "", "")
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	dashAfter, err := desk.Dashboard(ctx)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if got := tab(after, orders.QueueAwaitingPayment) - tab(before, orders.QueueAwaitingPayment); got != 1 {
		t.Errorf("the awaiting-payment tab grew by %d, want 1: a funded order is not awaiting payment", got)
	}
	if got := tab(after, orders.QueueReady) - tab(before, orders.QueueReady); got != 1 {
		t.Errorf("the ready tab grew by %d, want 1", got)
	}
	if got := dashAfter.PendingOrders - dashBefore.PendingOrders; got != 1 {
		t.Errorf("the awaiting-payment tile grew by %d, want 1", got)
	}
	if got := dashAfter.ReadyOrders - dashBefore.ReadyOrders; got != 1 {
		t.Errorf("the ready tile grew by %d, want 1", got)
	}

	for status, want := range map[orders.QueueFilter]struct{ in, out string }{
		orders.QueueAwaitingPayment: {in: unpaid, out: funded},
		orders.QueueReady:           {in: funded, out: unpaid},
	} {
		view, err := queue.List(ctx, status, "")
		if err != nil {
			t.Fatalf("Orders(%s): %v", status, err)
		}
		var sawIn, sawOut bool
		for _, o := range view.Orders {
			sawIn = sawIn || o.Number == want.in
			sawOut = sawOut || o.Number == want.out
		}
		if !sawIn || sawOut {
			t.Errorf("tab %q lists the expected order = %t and the other = %t, want true and false", status, sawIn, sawOut)
		}
	}
}

// The landing page reads its newest orders with a query of its own, and the
// queue reads its rows with another. Whatever state an order is in (unpaid,
// funded, paid by credit alone, returned in part, or in full with the refund
// settled or not) both desks must show the same row for it.
func TestStore_RecentOrdersMatchTheQueue(t *testing.T) {
	isolated := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, isolated)
	queue := admintest.OrderStore(isolated, admintest.Refunder{}, nil, nil)
	desk := storeOver(isolated, health.NewStore(isolated))

	numbers := map[string]string{} // case -> order number
	numbers["unpaid"] = admintest.PlaceUnpaidOrder(t, isolated)
	numbers["funded"], _, _ = admintest.PaidUnshippedOrder(t, isolated, 500000, 0, false)
	numbers["credit"], _ = admintest.PlaceOrderPaidByCredit(t, isolated, 0)
	for _, tc := range []struct {
		name     string
		returned int32
		refunded bool // the payout wrote the refunded event
	}{
		{"one unit of two, refunded", 1, true},
		{"both units, the card refund not yet settled", 2, false},
		{"both units, refunded", 2, true},
	} {
		returnID, number := admintest.ReturnedOrder(t, isolated, tc.returned)
		if _, err := isolated.Exec(ctx, `
			UPDATE return_requests SET status = 'approved', decided_at = now() WHERE id = $1`, returnID); err != nil {
			t.Fatalf("approve return: %v", err)
		}
		if tc.refunded {
			if _, err := isolated.Exec(ctx, `
				INSERT INTO order_events (order_id, kind, return_request_id)
				SELECT order_id, 'refunded', id FROM return_requests WHERE id = $1`, returnID); err != nil {
				t.Fatalf("record the refund: %v", err)
			}
		}
		numbers[tc.name] = number
	}

	view, err := desk.Dashboard(ctx)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	listed := map[string]orders.OrderRow{}
	for _, row := range view.Recent {
		listed[row.Number] = orders.OrderRow(row)
	}
	for name, number := range numbers {
		onDashboard, ok := listed[number]
		if !ok {
			t.Errorf("%s: the dashboard's recent orders lack %s", name, number)
			continue
		}
		found, err := queue.List(ctx, orders.QueueAll, number)
		if err != nil {
			t.Fatalf("List(%s): %v", number, err)
		}
		if len(found.Orders) != 1 {
			t.Fatalf("%s: the queue lists %d rows for %s, want 1", name, len(found.Orders), number)
		}
		if onDashboard != found.Orders[0] {
			t.Errorf("%s: the dashboard shows %+v, the queue shows %+v, want the same row", name, onDashboard, found.Orders[0])
		}
	}
	if got := listed[numbers["both units, refunded"]].StatusText; got != i18n.T(ctx, i18n.KeyStatusRefunded) {
		t.Errorf("an order returned in full and refunded reads %q on the dashboard, want %q", got, i18n.T(ctx, i18n.KeyStatusRefunded))
	}
	if got := listed[numbers["both units, the card refund not yet settled"]].StatusText; got == i18n.T(ctx, i18n.KeyStatusRefunded) {
		t.Errorf("an order whose refund has not settled reads %q on the dashboard, want its delivery word", got)
	}
}
