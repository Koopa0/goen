//go:build integration

package dashboard_test

import (
	"testing"
	"time"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/dashboard"
	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
)

// TestStore_DashboardQueueTabs: the dashboard and the queue tabs split pending the same way.
func TestStore_DashboardQueueTabs(t *testing.T) {
	ctx := t.Context()
	queue := admintest.OrderStore(pool, admintest.HealthyGateway(t), nil)
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
	before, err := queue.List(ctx, "", "", "")
	if err != nil {
		t.Fatalf("Orders: %v", err)
	}
	dashBefore, err := desk.Dashboard(ctx)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}

	unpaid := admintest.PlaceUnpaidOrder(t, pool)
	funded, _, _ := admintest.PaidUnshippedOrder(t, pool, 500000, 0, false)

	after, err := queue.List(ctx, "", "", "")
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
		view, err := queue.List(ctx, status, "", "")
		if err != nil {
			t.Fatalf("Orders(%s): %v", status, err)
		}
		var sawIn, sawOut bool
		for _, o := range view.Orders {
			sawIn = sawIn || o.Number == want.in
			sawOut = sawOut || o.Number == want.out
		}
		for _, w := range view.Work { // the packing tabs fill Work, the others Orders
			sawIn = sawIn || w.Number == want.in
			sawOut = sawOut || w.Number == want.out
		}
		if !sawIn || sawOut {
			t.Errorf("tab %q lists the expected order = %t and the other = %t, want true and false", status, sawIn, sawOut)
		}
	}
}

// TestStore_DashboardWaitAgreesWithTabs: a task's wait is the wait the first row of the tab it opens states.
// Both count shop days from a timestamp (shoptime.DaysAgo). The pool is the
// test's own, so each tab's first row is the order placed here. The credit
// order is placed three days and the time since today's shop midnight and a
// minute ago: that is four shop days and just under four 24-hour spans, so
// counting the wait in 24-hour spans of the database clock reads 3 and fails.
// The picking order was paid five days ago and taken into picking now, so
// ageing it from the picking event reads 0 and fails. Mutation left to CI.
func TestStore_DashboardWaitAgreesWithTabs(t *testing.T) {
	isolated := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, isolated)
	queue := admintest.OrderStore(isolated, admintest.HealthyGateway(t), nil)
	desk := storeOver(isolated, health.NewStore(isolated))

	now := admintest.DatabaseNow(t, isolated)
	sinceMidnight := now.Sub(shoptime.Midnight(now))
	admintest.PlaceOrderPaidByCredit(t, isolated, 3*24*time.Hour+sinceMidnight+time.Minute)
	_, pickingID, _ := admintest.PaidUnshippedOrder(t, isolated, 500000, 0, true)
	if _, err := isolated.Exec(ctx, `
		INSERT INTO order_events (order_id, kind, occurred_at) VALUES
		    ($1, 'paid', now() - interval '5 days'),
		    ($1, 'picking', now())`,
		pickingID); err != nil {
		t.Fatalf("record paying and picking: %v", err)
	}

	dash, err := desk.Dashboard(ctx)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	for _, tc := range []struct {
		label    i18n.Key
		tab      orders.QueueFilter
		wantDays int
	}{
		{i18n.KeyAdminStatusReadyToPick, orders.QueueReady, 4},
		{i18n.KeyAdminStatusPicking, orders.QueuePicking, 5},
	} {
		tabView, listErr := queue.List(ctx, tc.tab, "", "")
		if listErr != nil {
			t.Fatalf("List(%s): %v", tc.tab, listErr)
		}
		if len(tabView.Work) != 1 {
			t.Fatalf("the %q tab lists %d rows, want the one order placed here", tc.tab, len(tabView.Work))
		}
		var task *dashboard.Task
		for i := range dash.Tasks {
			if dash.Tasks[i].Label == tc.label {
				task = &dash.Tasks[i]
			}
		}
		if task == nil {
			t.Fatalf("the dashboard lists no %v task", tc.label)
		}
		first := tabView.Work[0]
		if task.WaitDays != first.WaitDays || task.Overdue != first.Overdue {
			t.Errorf("%q: the dashboard says %d days, overdue %t; the first row of the tab says %d days, overdue %t",
				tc.tab, task.WaitDays, task.Overdue, first.WaitDays, first.Overdue)
		}
		if first.WaitDays != tc.wantDays {
			t.Errorf("%q: the tab says %d days, want %d", tc.tab, first.WaitDays, tc.wantDays)
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
	queue := admintest.OrderStore(isolated, admintest.HealthyGateway(t), nil)
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
	listed := map[string]orders.Row{}
	for _, row := range view.Recent {
		listed[row.Number] = orders.Row(row)
	}
	for name, number := range numbers {
		onDashboard, ok := listed[number]
		if !ok {
			t.Errorf("%s: the dashboard's recent orders lack %s", name, number)
			continue
		}
		found, err := queue.List(ctx, orders.QueueAll, number, "")
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
