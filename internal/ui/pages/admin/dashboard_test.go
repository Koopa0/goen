package admin

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/chart"
	"github.com/koopa0/goen/internal/ui/components"
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
	if got := (&DashboardView{}).DeskTasks(); len(got) != 0 {
		t.Errorf("DeskTasks() with nothing waiting = %v, want none", got)
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

func TestAwaitingPaymentAndPickingAreTasks(t *testing.T) {
	t.Parallel()
	want := []Task{
		{Label: i18n.KeyAdminStatusPicking, Count: 2, Href: "/admin/orders?status=picking", HasAge: true, AgeSeconds: 7200},
		{Label: i18n.KeyAdminQueueStatPending, Count: 4, Href: "/admin/orders?status=pending"},
	}
	if got := (&DashboardView{PendingOrders: 4, PickingOrders: 2, PickingOldestSeconds: 7200}).DeskTasks(); !slices.Equal(got, want) {
		t.Errorf("DeskTasks() = %v, want %v", got, want)
	}
}

func TestTheDashboardFiguresAreLinkedLabelsBeforeTheirValues(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Dashboard(Meta(ctx), DashboardView{
		Week:   busyWeek(),
		Latest: &LatestPaid{Number: "GO-261005-000006", TotalCents: 128000, Elapsed: 38 * time.Minute},
	}))
	for _, want := range []struct {
		href  string
		label i18n.Key
		value string
	}{
		{"/admin/reports?days=7", i18n.KeyAdminRepRevenue, "<small class=\"ui-statline__pre\">NT$</small>59,006"},
		{"/admin/reports?days=7", i18n.KeyAdminRepPaidOrders, "44"},
		{"/admin/orders/GO-261005-000006", i18n.KeyAdminQueueLatestPaid, "38\u00a0<small>分鐘前</small>"},
	} {
		got := `<dt><a href="` + want.href + `">` + i18n.T(ctx, want.label) + `</a></dt><dd>` + want.value
		if !strings.Contains(html, got) {
			t.Errorf("Dashboard does not carry %s", got)
		}
	}
}

// busyWeek has 44 orders worth NT$59,006 this week against 31 worth NT$41,650,
// every day of both with an order.
func busyWeek() Week {
	days := func(values ...int64) chart.Series {
		s := chart.Series{}
		for i, v := range values {
			s.Buckets = append(s.Buckets, chart.Bucket{Day: time.Date(2026, 9, 29+i, 0, 0, 0, 0, time.UTC), Value: v})
		}
		return s
	}
	return Week{
		Orders: 44, RevenueCents: 5900600, RevenueSquares: 1,
		Previous:    PreviousFigures{Orders: 31, RevenueCents: 4165000, RevenueSquares: 1},
		RevenueDays: chart.SparklineProps{Previous: days(1, 2, 3, 4, 5, 6, 7), Current: days(1, 2, 3, 4, 5, 6, 7)},
		OrderDays:   chart.SparklineProps{Previous: days(1, 2, 3, 4, 5, 6, 7), Current: days(1, 2, 3, 4, 5, 6, 7)},
	}
}

func TestTheDashboardDrawsTheWeekOnlyWhenItHasDaysWithOrders(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	if html := renderToString(t, Dashboard(Meta(ctx), DashboardView{Week: busyWeek()})); strings.Count(html, "goen-spark__bar ") != 28 {
		t.Errorf("a busy week draws %d bars, want 28: two figures of 14 days", strings.Count(html, "goen-spark__bar "))
	}
	sparse := Week{Orders: 2, RevenueCents: 20000, RevenueSquares: 1, Previous: PreviousFigures{Orders: 1, RevenueCents: 10000}}
	sparse.OrderDays.Current.Buckets = []chart.Bucket{{Value: 1}, {Value: 1}}
	if html := renderToString(t, Dashboard(Meta(ctx), DashboardView{Week: sparse})); strings.Contains(html, "goen-spark") {
		t.Error("a week with orders on two days draws a sparkline")
	}
}

func TestTheLatestPaidOrderSaysWhatIsKnown(t *testing.T) {
	t.Parallel()
	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		for _, tc := range []struct {
			name string
			view DashboardView
			want string
		}{
			{"never paid", DashboardView{}, i18n.T(ctx, i18n.KeyAdminQueueLatestNone)},
			{"unreadable", DashboardView{LatestUnavailable: true}, i18n.T(ctx, i18n.KeyAdminQueueLatestUnavailable)},
			{"paid", DashboardView{Latest: &LatestPaid{Number: "GO-1", TotalCents: 128000}}, "GO-1 · NT$1,280"},
		} {
			if html := renderComponent(t, ctx, Dashboard(Meta(ctx), tc.view)); !strings.Contains(html, tc.want) {
				t.Errorf("%s in %v: the dashboard does not say %q", tc.name, loc, tc.want)
			}
		}
	}
}

func TestTheDashboardSaysWhenItCouldNotReadTheWeek(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	notice := i18n.T(ctx, i18n.KeyAdminQueueWeekUnavailable)
	for _, unavailable := range []bool{true, false} {
		html := renderComponent(t, ctx, Dashboard(Meta(ctx), DashboardView{WeekUnavailable: unavailable}))
		if got := strings.Contains(html, notice); got != unavailable {
			t.Errorf("Dashboard(WeekUnavailable=%v) shows the notice = %v", unavailable, got)
		}
		if !unavailable && !strings.Contains(html, i18n.T(ctx, i18n.KeyAdminRepRevenue)) {
			t.Error("a week without orders shows no revenue figure: it would read as a page without a week")
		}
	}
}

func TestElapsedValueUsesTheLargestWholeUnit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		d    time.Duration
		loc  i18n.Locale
		want string
	}{
		{"under a minute", 59 * time.Second, i18n.En, "Just now"},
		{"one minute", time.Minute, i18n.En, "1\u00a0<small>minute ago</small>"},
		{"minutes", 38 * time.Minute, i18n.En, "38\u00a0<small>minutes ago</small>"},
		{"minutes in Chinese", 38 * time.Minute, i18n.ZhHant, "38\u00a0<small>分鐘前</small>"},
		{"exactly an hour", time.Hour, i18n.En, "1\u00a0<small>hour ago</small>"},
		{"an hour and a half", 90 * time.Minute, i18n.En, "1\u00a0<small>hour ago</small>"},
		{"hours", 3*time.Hour + 59*time.Minute, i18n.En, "3\u00a0<small>hours ago</small>"},
		{"days", 49 * time.Hour, i18n.En, "2\u00a0<small>days ago</small>"},
	} {
		ctx := i18n.WithLocale(t.Context(), tc.loc)
		stat := components.GlanceStatLine([]components.LinkedStat{{Stat: components.Stat{Label: "x", Value: elapsedValue(ctx, tc.d)}, Href: "/"}})
		if html := renderComponent(t, ctx, stat); !strings.Contains(html, "<dd>"+tc.want) {
			t.Errorf("%s: elapsedValue(%v) renders %s, want <dd>%s", tc.name, tc.d, html, tc.want)
		}
	}
}

func TestTheWeekIsComparedAsTheReportCompares(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	// 44 against 31 orders is 13 apart, under two standard errors of 75.
	busy := busyWeek()
	if got, want := busy.ordersAgainst(ctx), i18n.Count(ctx, i18n.KeyAdminRepPrevious, 7, 7, "31"); got != want {
		t.Errorf("ordersAgainst inside the noise = %q, want %q", got, want)
	}
	big := Week{Orders: 300, RevenueCents: 3000000, RevenueSquares: 1, Previous: PreviousFigures{Orders: 100, RevenueCents: 1000000, RevenueSquares: 1}}
	if got, want := big.revenueAgainst(ctx), i18n.Count(ctx, i18n.KeyAdminRepMore, 7, 7, 200); got != want {
		t.Errorf("revenueAgainst of a tripled week = %q, want %q", got, want)
	}
	noisy := Week{Orders: 30, RevenueCents: 3100000, RevenueSquares: 1e18, Previous: PreviousFigures{Orders: 30, RevenueCents: 3000000, RevenueSquares: 1e18}}
	if got, want := noisy.revenueAgainst(ctx), i18n.Count(ctx, i18n.KeyAdminRepPrevious, 7, 7, "NT$30,000"); got != want {
		t.Errorf("revenueAgainst inside the noise = %q, want %q", got, want)
	}
}

func TestTheWeekStatsKeyTheTwoPeriodsUnderTheirBars(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := DashboardView{Week: busyWeek()}
	html := renderComponent(t, ctx, components.GlanceStatLine(view.WeekStats(ctx)))
	for _, key := range []string{"Previous 7 days", "Last 7 days"} {
		if got := strings.Count(html, "<span>"+key+"</span>"); got != 2 {
			t.Errorf("%q is a key under %d of the two small charts, want 2", key, got)
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

		t.Run(string(loc)+" nothing running out says so", func(t *testing.T) {
			t.Parallel()
			none := i18n.Count(ctx, i18n.KeyAdminQueueRunwayNone, coverWarnDays, coverWarnDays)
			unknown := i18n.T(ctx, i18n.KeyAdminQueueRunwayUnknown)
			noStock := i18n.Count(ctx, i18n.KeyAdminQueueRunwayNoStock, CoverWindowDays, CoverWindowDays)
			all := i18n.T(ctx, i18n.KeyAdminQueueRunwayAll)
			going := []StockRisk{{SKU: "EST-1", Name: "Going", Slug: "going", Sellable: 9, Sold: 60, Orders: 30, InStock: stockedAllWindow}}

			for _, tc := range []struct {
				name string
				view DashboardView
				want []string
				not  []string
			}{
				{"estimated and none running out", DashboardView{SoldOut: 3, RunwayBasis: RunwayEstimated}, []string{none, all}, []string{unknown, noStock}},
				{"nothing can be estimated", DashboardView{SoldOut: 3}, []string{unknown, all}, []string{none, noStock}},
				{"everything is sold out", DashboardView{SoldOut: 3, RunwayBasis: RunwayNothingInStock}, []string{noStock, all}, []string{none, unknown}},
				{"rows listed", DashboardView{Runway: going, RunwayBasis: RunwayEstimated}, []string{all}, []string{none, unknown, i18n.T(ctx, i18n.KeyAdminQueueRunwayRest)}},
				{"rows left off", DashboardView{Runway: going, RunwayCut: true, RunwayBasis: RunwayEstimated}, []string{i18n.T(ctx, i18n.KeyAdminQueueRunwayRest)}, []string{none, unknown, all}},
			} {
				html := render(tc.view)
				for _, want := range tc.want {
					if !strings.Contains(html, want) {
						t.Errorf("%s: the dashboard lacks %q", tc.name, want)
					}
				}
				for _, not := range tc.not {
					if strings.Contains(html, not) {
						t.Errorf("%s: the dashboard says %q", tc.name, not)
					}
				}
				if !strings.Contains(html, `href="/admin/reports#stock"`) {
					t.Errorf("%s: the runway section does not link to the report", tc.name)
				}
			}

			html := render(DashboardView{SoldOut: 3})
			for _, gone := range []string{"goen-report__rows", "goen-chartrangebar", `id="low-heading"`, "/admin/stock/", "/admin/stock?soldout=1"} {
				if strings.Contains(html, gone) {
					t.Errorf("the dashboard with nothing to estimate renders %q", gone)
				}
			}
		})
	}
}

func TestDashboardRunwayKeepsOnlyRowsRunningOutWithinTheLine(t *testing.T) {
	t.Parallel()

	// 9 sellable at 30 units over 30 days is 9 days; 45 sellable is 45 days.
	runningOut := func(n int) (rows []StockRisk) {
		for i := range n {
			rows = append(rows, StockRisk{SKU: fmt.Sprintf("RUN-%d", i), Sellable: 9, Sold: 30, Orders: 20, InStock: stockedAllWindow})
		}
		return rows
	}
	later := func(n int) (rows []StockRisk) {
		for i := range n {
			rows = append(rows, StockRisk{SKU: fmt.Sprintf("LATER-%d", i), Sellable: 45, Sold: 30, Orders: 20, InStock: stockedAllWindow})
		}
		return rows
	}
	soldOut := func(n int) (rows []StockRisk) {
		for i := range n {
			rows = append(rows, StockRisk{SKU: fmt.Sprintf("OUT-%d", i)})
		}
		return rows
	}
	fewOrders := []StockRisk{{SKU: "FEW-0", Sellable: 4, Sold: 6, Orders: 3, InStock: stockedAllWindow}}
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
		// wantBasis is what an empty list can say.
		wantBasis RunwayBasis
	}{
		{"sold out and running out", append(soldOut(4), runningOut(3)...), []string{"RUN-0", "RUN-1", "RUN-2"}, false, RunwayEstimated},
		{"only sold out", soldOut(6), nil, false, RunwayNothingInStock},
		{"sold out and rows too few to estimate", append(soldOut(2), fewOrders...), nil, false, RunwayTooFewSales},
		{"nothing listed", nil, nil, false, RunwayTooFewSales},
		{"estimates past the line are left out", append(runningOut(2), append(later(3), fewOrders...)...), []string{"RUN-0", "RUN-1"}, false, RunwayEstimated},
		{"only estimates past the line", later(4), nil, false, RunwayEstimated},
		{"rows that cannot be estimated are left out", fewOrders, nil, false, RunwayTooFewSales},
		{"seven running out", runningOut(7), []string{"RUN-0", "RUN-1", "RUN-2", "RUN-3", "RUN-4"}, true, RunwayEstimated},
		{"five running out", runningOut(5), []string{"RUN-0", "RUN-1", "RUN-2", "RUN-3", "RUN-4"}, false, RunwayEstimated},
		{"a later row does not count as one left off", append(runningOut(5), later(2)...), []string{"RUN-0", "RUN-1", "RUN-2", "RUN-3", "RUN-4"}, false, RunwayEstimated},
	} {
		kept, cut, basis := DashboardRunway(tc.listed)
		if got := skus(kept); !slices.Equal(got, tc.want) || cut != tc.wantCut || basis != tc.wantBasis {
			t.Errorf("%s: DashboardRunway kept %v, cut %t, basis %d, want %v, cut %t, basis %d", tc.name, got, cut, basis, tc.want, tc.wantCut, tc.wantBasis)
		}
	}
}
