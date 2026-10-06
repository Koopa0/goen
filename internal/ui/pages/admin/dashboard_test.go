package admin

import (
	"slices"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
)

// TestTheDashboardOpensEveryOrderItLists holds the landing page's reason for
// carrying a queue at all. A row that names an order and does not open it makes
// a staff member copy the number into the order screen's search box, which is
// the work the dashboard exists to remove.
func TestTheDashboardOpensEveryOrderItLists(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	t.Run("a listed order is a link to itself", func(t *testing.T) {
		t.Parallel()
		html := renderToString(t, Dashboard(Meta(ctx), DashboardView{
			Recent: []OrderRow{{
				Number:     "GO-260918-000001",
				Status:     order.FulfillmentShipped,
				StatusText: "已出貨",
				PlacedAt:   "2026-09-18 10:00",
				Recipient:  "版面顧客",
				TotalCents: 149900,
			}},
		}))
		if !strings.Contains(html, `href="/admin/orders/GO-260918-000001"`) {
			t.Error("the queue names an order it does not open")
		}
		if !strings.Contains(html, "GO-260918-000001") {
			t.Fatal("the queue does not list the order at all")
		}
	})

	t.Run("an empty queue says so rather than showing an empty table", func(t *testing.T) {
		t.Parallel()
		html := renderToString(t, Dashboard(Meta(ctx), DashboardView{}))
		if !strings.Contains(html, i18n.T(ctx, i18n.KeyAdminQueueNoneYet)) {
			t.Error("an order-less dashboard does not say there are no orders yet")
		}
	})
}

// TestTheDashboardListsOnlyWhatWaitsForAPerson holds the list the five work
// tiles gave way to: nothing is drawn when nothing waits, and each kind that
// does is a link to where it is done.
func TestTheDashboardListsOnlyWhatWaitsForAPerson(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	t.Run("nothing waiting draws no list", func(t *testing.T) {
		t.Parallel()
		html := renderToString(t, Dashboard(Meta(ctx), DashboardView{}))
		if strings.Contains(html, i18n.T(ctx, i18n.KeyAdminQueueTasksHeading)) || strings.Contains(html, "goen-admin__task") {
			t.Error("an idle dashboard draws a task list")
		}
	})

	t.Run("each task is a link with its count", func(t *testing.T) {
		t.Parallel()
		html := renderToString(t, Dashboard(Meta(ctx), DashboardView{Tasks: []Task{
			{Label: i18n.KeyAdminHPClaimsHeading, Count: 52, Href: "/admin/health"},
			{Label: i18n.KeyAdminQueueStatQuestions, Count: 7, Href: "/admin/questions"},
		}}))
		for _, want := range []string{
			i18n.T(ctx, i18n.KeyAdminQueueTasksHeading),
			`href="/admin/health"`, i18n.T(ctx, i18n.KeyAdminHPClaimsHeading), ">52<",
			`href="/admin/questions"`, i18n.T(ctx, i18n.KeyAdminQueueStatQuestions), ">7<",
		} {
			if !strings.Contains(html, want) {
				t.Errorf("the task list does not carry %q", want)
			}
		}
	})
}

func TestDeskTasksLeaveOutWhatIsNotWaiting(t *testing.T) {
	t.Parallel()
	if got := (&DashboardView{PendingOrders: 4, PickingOrders: 2, ActiveProducts: 9}).DeskTasks(); len(got) != 0 {
		t.Errorf("DeskTasks() with only figures = %v, want none", got)
	}
	v := &DashboardView{UninspectedReturns: 2, UnansweredQuestions: 1}
	want := []Task{
		{Label: i18n.KeyAdminQueueTaskUninspected, Count: 2, Href: "/admin/returns"},
		{Label: i18n.KeyAdminQueueStatQuestions, Count: 1, Href: "/admin/questions"},
	}
	if got := v.DeskTasks(); !slices.Equal(got, want) {
		t.Errorf("DeskTasks() = %v, want %v", got, want)
	}
}

func TestWorkerHealthTasksFollowTheHealthPredicates(t *testing.T) {
	t.Parallel()
	if got := (&WorkerHealthView{}).Tasks(); len(got) != 0 {
		t.Errorf("Tasks() of a healthy view = %v, want none", got)
	}
	v := &WorkerHealthView{
		UnreconciledPayments: 1, StrandedClaimCount: 73, UninvoicedCount: 2,
		CancelledOrderInvoiceCount: 3, OpenRefundCount: 4,
		ExpiredHolds: 51, MaxExpiredHolds: 50,
	}
	want := []Task{
		{Label: i18n.KeyAdminHPUnreconciledHeading, Count: 1, Href: "/admin/health"},
		{Label: i18n.KeyAdminHPClaimsHeading, Count: 73, Href: "/admin/health"},
		{Label: i18n.KeyAdminHPUninvoicedHeading, Count: 2, Href: "/admin/health"},
		{Label: i18n.KeyAdminHPCancelledOrderInvoicesHeading, Count: 3, Href: "/admin/health"},
		{Label: i18n.KeyAdminHPOpenRefundsHeading, Count: 4, Href: "/admin/health"},
		{Label: i18n.KeyAdminQueueTaskHolds, Count: 51, Href: "/admin/health"},
	}
	if got := v.Tasks(); !slices.Equal(got, want) {
		t.Errorf("Tasks() = %v, want %v", got, want)
	}
	if got := (&WorkerHealthView{ExpiredHolds: 50, MaxExpiredHolds: 50}).Tasks(); len(got) != 0 {
		t.Errorf("Tasks() with holds at the threshold = %v, want none: the health page calls that healthy", got)
	}
}

// TestTheDashboardSaysWhenItCouldNotCheckTheHealthDesk holds that an absent
// payment or invoice task is only read as "nothing to check" when the desk was
// actually read.
func TestTheDashboardSaysWhenItCouldNotCheckTheHealthDesk(t *testing.T) {
	t.Parallel()
	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		notice := i18n.T(ctx, i18n.KeyAdminQueueHealthUnavailable)
		for _, unavailable := range []bool{true, false} {
			html := renderComponent(t, ctx, Dashboard(Meta(ctx), DashboardView{HealthUnavailable: unavailable}))
			if got := strings.Contains(html, notice); got != unavailable {
				t.Errorf("Dashboard(HealthUnavailable=%v) in %v shows the notice = %v", unavailable, loc, got)
			}
		}
	}
}

func TestTheDashboardFiguresAreLinkedLabelsBeforeTheirValues(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Dashboard(Meta(ctx), DashboardView{PendingOrders: 4, PickingOrders: 2, ActiveProducts: 9}))
	for _, want := range []struct {
		href  string
		label i18n.Key
		value string
	}{
		{"/admin/orders?status=pending", i18n.KeyAdminQueueStatPending, "4"},
		{"/admin/orders?status=picking", i18n.KeyAdminStatusPicking, "2"},
		{"/admin/products", i18n.KeyAdminQueueStatActive, "9"},
	} {
		got := `<dt><a href="` + want.href + `">` + i18n.T(ctx, want.label) + `</a></dt><dd>` + want.value
		if !strings.Contains(html, got) {
			t.Errorf("Dashboard does not carry %s", got)
		}
	}
}
