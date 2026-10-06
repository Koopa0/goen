package admin

import (
	"fmt"
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
			{Label: i18n.KeyAdminQueueTaskClaims, Count: 52, Href: "/admin/health#claims-heading", HasAge: true, AgeSeconds: 0},
			{Label: i18n.KeyAdminQueueStatQuestions, Count: 7, Href: "/admin/questions", HasAge: true, AgeSeconds: 3*86400 + 1},
			{Label: i18n.KeyAdminQueueStatSoldOut, Count: 2, Href: "/admin/reports#stock"},
		}}))
		for _, want := range []string{
			i18n.T(ctx, i18n.KeyAdminQueueTasksHeading),
			`href="/admin/health#claims-heading"`, i18n.T(ctx, i18n.KeyAdminQueueTaskClaims), ">52<", "不到 1 天",
			`href="/admin/questions"`, i18n.T(ctx, i18n.KeyAdminQueueStatQuestions), ">7<", "最久 3 天",
			`href="/admin/reports#stock"`, ">2<",
		} {
			if !strings.Contains(html, want) {
				t.Errorf("the task list does not carry %q", want)
			}
		}
		_, lowStock, found := strings.Cut(html, `href="/admin/reports#stock"`)
		if !found {
			t.Fatal("the sold-out task is not listed")
		}
		lowStock, _, _ = strings.Cut(lowStock, "</li>")
		if strings.Contains(lowStock, "goen-admin__taskage") {
			t.Error("the sold-out task, which has no start time, carries an age")
		}
	})

	t.Run("a task that is wrong rather than waiting is marked", func(t *testing.T) {
		t.Parallel()
		html := renderToString(t, Dashboard(Meta(ctx), DashboardView{Tasks: []Task{
			{Label: i18n.KeyAdminQueueTaskPayments, Count: 1, Href: "/admin/health#events-heading", Alert: true},
			{Label: i18n.KeyAdminQueueStatMessages, Count: 1, Href: "/admin/messages"},
		}}))
		if got := strings.Count(html, "goen-admin__task--alert"); got != 1 {
			t.Errorf("alert marks = %d, want 1: only the health task", got)
		}
	})
}

func TestDeskTasksLeaveOutWhatIsNotWaiting(t *testing.T) {
	t.Parallel()
	if got := (&DashboardView{PendingOrders: 4, PickingOrders: 2, ActiveProducts: 9}).DeskTasks(); len(got) != 0 {
		t.Errorf("DeskTasks() with only figures = %v, want none", got)
	}
	v := &DashboardView{
		UninspectedReturns: 2, UninspectedReturnsOldestSeconds: 90,
		UnansweredQuestions: 1, UnansweredQuestionsOldestSeconds: 30,
	}
	want := []Task{
		{Label: i18n.KeyAdminQueueTaskUninspected, Count: 2, Href: "/admin/returns", HasAge: true, AgeSeconds: 90},
		{Label: i18n.KeyAdminQueueStatQuestions, Count: 1, Href: "/admin/questions", HasAge: true, AgeSeconds: 30},
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
		StrandedClaimOldestSeconds: 100, UninvoicedOldestSeconds: 200, CancelledOrderInvoiceOldestSeconds: 300,
		ExpiredHolds: 51, MaxExpiredHolds: 50,
	}
	want := []Task{
		{Label: i18n.KeyAdminQueueTaskPayments, Count: 1, Href: "/admin/health#events-heading", Alert: true},
		{Label: i18n.KeyAdminQueueTaskClaims, Count: 73, Href: "/admin/health#claims-heading", Alert: true, HasAge: true, AgeSeconds: 100},
		{Label: i18n.KeyAdminQueueTaskUninvoiced, Count: 2, Href: "/admin/health#uninvoiced-heading", Alert: true, HasAge: true, AgeSeconds: 200},
		{Label: i18n.KeyAdminHPCancelledOrderInvoicesHeading, Count: 3, Href: "/admin/health#cancelled-order-invoices-heading", Alert: true, HasAge: true, AgeSeconds: 300},
		{Label: i18n.KeyAdminHPOpenRefundsHeading, Count: 4, Href: "/admin/health#refunds-heading", Alert: true},
		{Label: i18n.KeyAdminQueueTaskHolds, Count: 51, Href: "/admin/health", Alert: true},
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

// The dashboard's stock section is the days cover, not the low-stock table the
// sold-out task row already names.
func TestTheDashboardShowsTheDaysCoverInPlaceOfTheLowStockList(t *testing.T) {
	t.Parallel()

	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		render := func(v DashboardView) string { return renderComponent(t, ctx, Dashboard(Meta(ctx), v)) }

		t.Run(string(loc)+" sold out row opens the report", func(t *testing.T) {
			t.Parallel()
			view := DashboardView{SoldOut: 3}
			html := render(DashboardView{Tasks: view.DeskTasks()})
			if !strings.Contains(html, `href="/admin/reports#stock"`) || !strings.Contains(html, i18n.T(ctx, i18n.KeyAdminQueueStatSoldOut)) {
				t.Error("the sold-out task row does not link to the report's stock section")
			}
			if strings.Contains(html, "/admin/stock?soldout=1") {
				t.Error("the dashboard still links to the low-stock list")
			}
		})

		t.Run(string(loc)+" rows of SKUs that are not sold out", func(t *testing.T) {
			t.Parallel()
			rows := []StockRisk{
				{SKU: "FEW-1", Name: "Rare", Slug: "rare", Sellable: 4, Sold: 6, Orders: 3, InStock: stockedAllWindow},
				{SKU: "EST-1", Name: "Going", Slug: "going", Sellable: 20, Sold: 60, Orders: 30, InStock: stockedAllWindow},
			}
			html := render(DashboardView{Runway: rows})
			for _, want := range []string{
				`id="runway-heading"`, "FEW-1", "EST-1", `href="/admin/products/going"`,
				i18n.T(ctx, i18n.KeyAdminRepFewSold), "goen-chartrangebar",
			} {
				if !strings.Contains(html, want) {
					t.Errorf("the runway section lacks %q", want)
				}
			}
			if strings.Contains(html, i18n.T(ctx, i18n.KeyAdminQueueRunwayRest)) {
				t.Error("the runway says there is more in the report although no row was left off")
			}
			if rest := render(DashboardView{Runway: rows, RunwayCut: true}); !strings.Contains(rest, i18n.T(ctx, i18n.KeyAdminQueueRunwayRest)) {
				t.Error("rows were left off and the runway does not point to the report")
			}
		})

		t.Run(string(loc)+" nothing to show leaves the section out", func(t *testing.T) {
			t.Parallel()
			html := render(DashboardView{SoldOut: 3})
			for _, gone := range []string{`id="runway-heading"`, `id="low-heading"`, "/admin/stock/", "/admin/stock?soldout=1"} {
				if strings.Contains(html, gone) {
					t.Errorf("the dashboard with nothing to estimate renders %q", gone)
				}
			}
		})
	}
}

func TestDashboardRunwayLeavesOutSoldOutRowsAndKeepsFive(t *testing.T) {
	t.Parallel()

	estimated := func(n int) (rows []StockRisk) {
		for i := range n {
			rows = append(rows, StockRisk{SKU: fmt.Sprintf("EST-%d", i), Sellable: 9, Sold: 30, Orders: 20, InStock: stockedAllWindow})
		}
		return rows
	}
	soldOut := func(n int) (rows []StockRisk) {
		for i := range n {
			rows = append(rows, StockRisk{SKU: fmt.Sprintf("OUT-%d", i)})
		}
		return rows
	}
	skus := func(rows []StockRisk) (out []string) {
		for _, r := range rows {
			out = append(out, r.SKU)
		}
		return out
	}

	for _, tc := range []struct {
		name    string
		listed  []StockRisk
		want    []string
		wantCut bool
	}{
		{"four sold out and three estimated", append(soldOut(4), estimated(3)...), []string{"EST-0", "EST-1", "EST-2"}, false},
		{"only sold out", soldOut(6), nil, false},
		{"seven estimated", estimated(7), []string{"EST-0", "EST-1", "EST-2", "EST-3", "EST-4"}, true},
		{"five estimated", estimated(5), []string{"EST-0", "EST-1", "EST-2", "EST-3", "EST-4"}, false},
		{"sold out first and six estimated", append(soldOut(5), estimated(6)...), []string{"EST-0", "EST-1", "EST-2", "EST-3", "EST-4"}, true},
	} {
		kept, cut := DashboardRunway(tc.listed)
		if got := skus(kept); !slices.Equal(got, tc.want) || cut != tc.wantCut {
			t.Errorf("%s: DashboardRunway kept %v, cut %t, want %v, cut %t", tc.name, got, cut, tc.want, tc.wantCut)
		}
	}
}
