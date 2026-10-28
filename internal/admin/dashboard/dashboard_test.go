package dashboard

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/admin/worklist"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/product/inventory"
	"github.com/koopa0/goen/internal/sales"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/chart"
	"github.com/koopa0/goen/internal/ui/stockrisk"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestDashboardOrderLinks holds the landing page's reason for
// carrying a queue at all. A row that names an order and does not open it makes
// a staff member copy the number into the order screen's search box, which is
// the work the dashboard exists to remove.
func TestDashboardOrderLinks(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	t.Run("a listed order is a link to itself", func(t *testing.T) {
		t.Parallel()
		html := renderToString(t, Dashboard(Meta(ctx), View{
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
		html := renderToString(t, Dashboard(Meta(ctx), View{}))
		if !strings.Contains(html, i18n.T(ctx, i18n.KeyAdminQueueNoneYet)) {
			t.Error("an order-less dashboard does not say there are no orders yet")
		}
	})
}

// TestDashboardWaitingTasks holds the list the five work
// tiles gave way to: a sentence when nothing waits, and each kind that does is
// a link to where it is done.
func TestDashboardWaitingTasks(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	t.Run("nothing waiting says so rather than drawing nothing", func(t *testing.T) {
		t.Parallel()
		html := renderToString(t, Dashboard(Meta(ctx), View{}))
		if !strings.Contains(html, i18n.T(ctx, i18n.KeyAdminQueueTasksHeading)) || !strings.Contains(html, i18n.T(ctx, i18n.KeyAdminQueueNothingWaiting)) {
			t.Error("an idle dashboard does not say that nothing is waiting")
		}
		if strings.Contains(html, "goen-admin__tasks") {
			t.Error("an idle dashboard draws a task list")
		}
	})

	t.Run("each task is a link with its count", func(t *testing.T) {
		t.Parallel()
		html := renderToString(t, Dashboard(Meta(ctx), View{Tasks: []Task{
			{Label: i18n.KeyAdminQueueTaskClaims, Count: 52, Href: "/admin/health#claims-heading", HasAge: true},
			{Label: i18n.KeyAdminQueueStatQuestions, Count: 7, Href: "/admin/questions", HasAge: true, WaitDays: 3},
			{Label: i18n.KeyAdminQueueStatSoldOut, Count: 2, Href: "/admin/reports#stock"},
		}}))
		for _, want := range []string{
			i18n.T(ctx, i18n.KeyAdminQueueTasksHeading),
			`href="/admin/health#claims-heading"`, i18n.T(ctx, i18n.KeyAdminQueueTaskClaims), ">52<", "都是今天的",
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
		html := renderToString(t, Dashboard(Meta(ctx), View{Tasks: []Task{
			{Label: i18n.KeyAdminQueueTaskPayments, Count: 1, Href: "/admin/health#events-heading", Alert: true},
			{Label: i18n.KeyAdminQueueStatMessages, Count: 1, Href: "/admin/messages"},
		}}))
		if got := strings.Count(html, "goen-admin__task--alert"); got != 1 {
			t.Errorf("alert marks = %d, want 1: only the health task", got)
		}
	})

	t.Run("only an overdue task says so, in words and not by colour", func(t *testing.T) {
		t.Parallel()
		for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
			ctx := i18n.WithLocale(t.Context(), loc)
			html := renderComponent(t, ctx, Dashboard(Meta(ctx), View{Tasks: []Task{
				{Label: i18n.KeyAdminStatusReadyToPick, Count: 4, Href: "/admin/orders?status=ready", HasAge: true, WaitDays: 3, Overdue: true},
				{Label: i18n.KeyAdminStatusPicking, Count: 1, Href: "/admin/orders?status=picking", HasAge: true, WaitDays: 1},
			}}))
			if got := strings.Count(html, i18n.T(ctx, i18n.KeyAdminOverdue)); got != 1 {
				t.Errorf("%v: the page says overdue %d times, want once: only the task that waited past the line", loc, got)
			}
			wantBadge(t, html, i18n.T(ctx, i18n.KeyAdminOverdue), "goen-badge goen-badge--warn")
			if !strings.Contains(html, `<span aria-hidden="true">▲</span>`) {
				t.Errorf("%v: the overdue mark has no triangle", loc)
			}
		}
	})
}

// shopNoon is noon on a shop day, which no zone shift moves to another day.
func shopNoon(t *testing.T, year int, month time.Month, day int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(shoptime.Zone)
	if err != nil {
		t.Fatalf("load %s: %v", shoptime.Zone, err)
	}
	return time.Date(year, month, day, 12, 0, 0, 0, loc)
}

// TestView_DeskTasksOmitsIdle: desk tasks leave out what is not waiting.
func TestView_DeskTasksOmitsIdle(t *testing.T) {
	t.Parallel()
	now := shopNoon(t, 2026, time.October, 29)
	if got := (&View{}).DeskTasks(now); len(got) != 0 {
		t.Errorf("DeskTasks() with nothing waiting = %v, want none", got)
	}
	v := &View{
		UninspectedReturns: 2, UninspectedReturnsOldestSince: now.AddDate(0, 0, -2),
		UnansweredQuestions: 1, UnansweredQuestionsOldestSince: now,
	}
	want := []Task{
		{Label: i18n.KeyAdminQueueTaskUninspected, Count: 2, Href: "/admin/returns", HasAge: true, WaitDays: 2},
		{Label: i18n.KeyAdminQueueStatQuestions, Count: 1, Href: "/admin/questions", HasAge: true, WaitDays: 0},
	}
	if got := v.DeskTasks(now); !slices.Equal(got, want) {
		t.Errorf("DeskTasks() = %v, want %v", got, want)
	}
}

// TestView_DeskTasksCountShopDays holds that a wait is counted in shop days from
// a moment, as the orders desk's work tabs count it (shoptime.DaysAgo), and not
// in spans of 24 hours: the list a task opens would otherwise say another number.
// Mutation: dividing the elapsed time by 24 hours turns the first two cases red.
func TestView_DeskTasksCountShopDays(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation(shoptime.Zone)
	if err != nil {
		t.Fatalf("load %s: %v", shoptime.Zone, err)
	}
	at := func(day, hour, minute int) time.Time {
		return time.Date(2026, time.October, day, hour, minute, 0, 0, loc)
	}
	for _, tc := range []struct {
		name       string
		since, now time.Time
		want       int
	}{
		{"an hour across midnight is a day", at(28, 23, 30), at(29, 0, 30), 1},
		{"two days less five minutes is two days", at(27, 0, 10), at(29, 0, 5), 2},
		{"a day and a half within one day is today", at(29, 0, 10), at(29, 23, 50), 0},
		{"a moment in the future is today", at(29, 12, 1), at(29, 12, 0), 0},
	} {
		v := &View{PendingReturns: 1, PendingReturnsOldestSince: tc.since}
		got := v.DeskTasks(tc.now)
		if len(got) != 1 || got[0].WaitDays != tc.want {
			t.Errorf("%s: waited %v, want %d days", tc.name, got, tc.want)
		}
	}
}

// TestView_DeskTasksOverdue holds the line past which a paid order waiting to be
// packed is late: order.PackWithinDays shop days, no sooner. Only the two order
// tasks have such a line.
// Mutation: PackingOverdue with >= turns the day-at-the-line case red; marking
// every task turns the returns case red.
func TestView_DeskTasksOverdue(t *testing.T) {
	t.Parallel()
	now := shopNoon(t, 2026, time.October, 29)
	ago := func(days int) time.Time { return now.AddDate(0, 0, -days) }
	for _, days := range []int{order.PackWithinDays - 1, order.PackWithinDays, order.PackWithinDays + 1} {
		v := &View{
			ReadyOrders: 1, ReadyOldestSince: ago(days),
			PickingOrders: 1, PickingOldestSince: ago(days),
			PendingReturns: 1, PendingReturnsOldestSince: ago(days + 10),
		}
		want := days > order.PackWithinDays
		for _, task := range v.DeskTasks(now) {
			orderTask := task.Label == i18n.KeyAdminStatusReadyToPick || task.Label == i18n.KeyAdminStatusPicking
			if task.Overdue != (orderTask && want) {
				t.Errorf("after %d days the task %v has Overdue = %t, want %t", days, task.Label, task.Overdue, orderTask && want)
			}
		}
	}
}

// TestHealthTasks_WaitFromNow: the health desk reports a wait in seconds, which
// the dashboard counts in shop days like every other task.
func TestHealthTasks_WaitFromNow(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation(shoptime.Zone)
	if err != nil {
		t.Fatalf("load %s: %v", shoptime.Zone, err)
	}
	now := time.Date(2026, time.October, 29, 0, 30, 0, 0, loc)
	got := healthTasks([]worklist.Task{
		{Label: i18n.KeyAdminQueueTaskClaims, Count: 1, Href: "/admin/health#claims-heading", Alert: true, HasAge: true, AgeSeconds: 3600},
		{Label: i18n.KeyAdminQueueTaskPayments, Count: 1, Href: "/admin/health#events-heading", Alert: true},
	}, now)
	want := []Task{
		{Label: i18n.KeyAdminQueueTaskClaims, Count: 1, Href: "/admin/health#claims-heading", Alert: true, HasAge: true, WaitDays: 1},
		{Label: i18n.KeyAdminQueueTaskPayments, Count: 1, Href: "/admin/health#events-heading", Alert: true},
	}
	if !slices.Equal(got, want) {
		t.Errorf("healthTasks = %+v, want %+v: an hour across midnight is a day, a task without a start has none", got, want)
	}
}

// TestDashboardHealthUnavailable holds that an absent
// payment or invoice task is only read as "nothing to check" when the desk was
// actually read.
func TestDashboardHealthUnavailable(t *testing.T) {
	t.Parallel()
	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		notice := i18n.T(ctx, i18n.KeyAdminQueueHealthUnavailable)
		for _, unavailable := range []bool{true, false} {
			html := renderComponent(t, ctx, Dashboard(Meta(ctx), View{HealthUnavailable: unavailable}))
			if got := strings.Contains(html, notice); got != unavailable {
				t.Errorf("Dashboard(HealthUnavailable=%v) in %v shows the notice = %v", unavailable, loc, got)
			}
		}
	}
}

// TestView_DeskTasksOrders: awaiting payment and picking are tasks.
func TestView_DeskTasksOrders(t *testing.T) {
	t.Parallel()
	now := shopNoon(t, 2026, time.October, 29)
	want := []Task{
		{Label: i18n.KeyAdminStatusPicking, Count: 2, Href: "/admin/orders?status=picking", HasAge: true, WaitDays: 0},
		{Label: i18n.KeyAdminQueueStatPending, Count: 4, Href: "/admin/orders?status=pending"},
	}
	if got := (&View{PendingOrders: 4, PickingOrders: 2, PickingOldestSince: now.Add(-2 * time.Hour)}).DeskTasks(now); !slices.Equal(got, want) {
		t.Errorf("DeskTasks() = %v, want %v", got, want)
	}
}

// TestDashboardFigureLinks: the dashboard figures are linked labels before their values.
// The two sales figures say which days they are of, because they sit under a
// chart of fourteen.
func TestDashboardFigureLinks(t *testing.T) {
	t.Parallel()
	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		html := renderComponent(t, ctx, Dashboard(Meta(ctx), View{
			Week:   busyWeek(),
			Latest: &LatestPaid{Number: "GO-261005-000006", TotalCents: 128000, Elapsed: 38 * time.Minute},
		}))
		for _, want := range []struct {
			href  string
			label string
		}{
			{"/admin/reports?days=7", i18n.Count(ctx, i18n.KeyAdminQueueRevenueDays, WeekDays, WeekDays)},
			{"/admin/reports?days=7", i18n.Count(ctx, i18n.KeyAdminQueuePaidOrdersDays, WeekDays, WeekDays)},
			{"/admin/orders/GO-261005-000006", i18n.T(ctx, i18n.KeyAdminQueueLatestPaid)},
		} {
			if got := `<dt><a href="` + want.href + `">` + want.label + `</a></dt><dd>`; !strings.Contains(html, got) {
				t.Errorf("%v: Dashboard does not carry %s", loc, got)
			}
		}
	}
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderComponent(t, ctx, Dashboard(Meta(ctx), View{Week: busyWeek()}))
	for _, want := range []string{"近 7 天營收", "<small class=\"ui-statline__pre\">NT$</small>59,006", "近 7 天已付款訂單"} {
		if !strings.Contains(html, want) {
			t.Errorf("Dashboard does not carry %q", want)
		}
	}
}

// busyWeek has 44 orders worth NT$59,006 this week against 31 worth NT$41,650.
func busyWeek() Week {
	return Week{
		Orders: 44, RevenueCents: 5900600, RevenueSquares: 1,
		Previous: sales.Period{Orders: 31, RevenueCents: 4165000, RevenueSquares: 1},
	}
}

// salesDays is DailyDays shop days ending 2026-10-29 with the revenue of each
// in cents, the last cut short.
func salesDays(values ...int64) chart.Series {
	s := chart.Series{Partial: true}
	for i, v := range values {
		s.Buckets = append(s.Buckets, chart.Bucket{Day: time.Date(2026, time.October, 29-len(values)+1+i, 0, 0, 0, 0, time.UTC), Value: v})
	}
	return s
}

// TestDashboardSalesChart: a fortnight with paid orders on enough days draws
// the revenue as columns, with the earlier week named and nothing bracketed;
// too few days say so in a sentence and draw no frame.
// Mutation: drawing the chart whatever the density turns the two-day case red;
// passing the campaigns' spans turns the no-bracket check red.
func TestDashboardSalesChart(t *testing.T) {
	t.Parallel()
	busy := salesDays(10000, 0, 25000, 30000, 0, 12000, 40000, 51000, 62000, 0, 48000, 70000, 11250000, 18400)
	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		render := func(daily chart.Series) string {
			return renderComponent(t, ctx, Dashboard(Meta(ctx), View{Daily: daily, ReadAt: time.Date(2026, time.October, 29, 14, 32, 0, 0, time.UTC)}))
		}

		html := render(busy)
		if got := strings.Count(html, "goen-chart__frame--columns"); got != 1 {
			t.Errorf("%v: a fortnight with paid orders draws %d column charts, want 1", loc, got)
		}
		if strings.Contains(html, "goen-chart__strip") || strings.Contains(html, "goen-chart__span ") {
			t.Errorf("%v: the dashboard's chart brackets a campaign", loc)
		}
		for _, want := range []string{
			i18n.T(ctx, i18n.KeyChartPeriod),
			i18n.Count(ctx, i18n.KeyAdminRepPreviousDays, WeekDays, WeekDays),
			"NT$112,500",
		} {
			if !strings.Contains(html, want) {
				t.Errorf("%v: the chart does not carry %q", loc, want)
			}
		}

		two := render(salesDays(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4000, 9000))
		if strings.Contains(two, "goen-chart__frame") {
			t.Errorf("%v: two days with paid orders draw a chart", loc)
		}
		if want := i18n.Count(ctx, i18n.KeyAdminQueueSalesFew, 2, 2, DailyDays); !strings.Contains(two, want) {
			t.Errorf("%v: two days with paid orders do not say %q", loc, want)
		}
		if none := render(salesDays(make([]int64, DailyDays)...)); !strings.Contains(none, i18n.Count(ctx, i18n.KeyAdminQueueSalesNone, DailyDays, DailyDays)) {
			t.Errorf("%v: a fortnight without paid orders does not say so", loc)
		}
	}
}

// TestView_SalesCaption: the chart's text does not
// tell the earlier week by its colour.
func TestView_SalesCaption(t *testing.T) {
	t.Parallel()
	v := &View{Daily: salesDays(10000, 0, 25000, 30000, 0, 12000, 40000, 51000, 62000, 0, 48000, 70000, 11250000, 18400)}
	zh := i18n.WithLocale(t.Context(), i18n.ZhHant)
	want := "每天的已付款營收。前 7 天是 10/16–10/22，最高是 10/28，NT$112,500。"
	if got := v.salesCaption(zh); got != want {
		t.Errorf("caption = %q, want %q", got, want)
	}
	en := i18n.WithLocale(t.Context(), i18n.En)
	if got := v.salesCaption(en); strings.Contains(got, "grey") || !strings.Contains(got, "Oct 16") {
		t.Errorf("caption = %q, want the dates of the previous 7 days and no colour", got)
	}
	if got := (&View{}).salesCaption(zh); got != "" {
		t.Errorf("caption of no days = %q, want none", got)
	}
}

// TestDashboardSellers: the best sellers of the week are drawn as the report
// draws them; a week that could not be read does not say nothing was sold.
func TestDashboardSellers(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	render := func(v View) string { return renderComponent(t, ctx, Dashboard(Meta(ctx), v)) }
	one := sales.Seller{Slug: "mug", Name: "馬克杯", Units: 3, RevenueCents: 90000}
	two := sales.Seller{Slug: "pot", Name: "茶壺", Units: 1, RevenueCents: 50000}
	empty := i18n.Count(ctx, i18n.KeyAdminQueueSellersNone, WeekDays, WeekDays)

	if html := render(View{}); !strings.Contains(html, empty) || strings.Contains(html, "goen-report__rows") {
		t.Error("a week with no sale does not say so, or draws a list")
	}
	if html := render(View{WeekUnavailable: true}); strings.Contains(html, empty) {
		t.Error("a week that could not be read says nothing was sold")
	}
	html := render(View{Sellers: []sales.Seller{one}})
	if !strings.Contains(html, `href="/admin/products/mug"`) || strings.Contains(html, "goen-chartbar") {
		t.Error("one seller is not a link to its product, or is drawn against nothing")
	}
	if html := render(View{Sellers: []sales.Seller{one, two}}); strings.Count(html, "goen-chartbar") < 2 {
		t.Error("two sellers are not drawn as bars")
	}
}

// TestDashboardLatestPaid: the latest paid order says what is known.
func TestDashboardLatestPaid(t *testing.T) {
	t.Parallel()
	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		for _, tc := range []struct {
			name string
			view View
			want string
		}{
			{"never paid", View{}, i18n.T(ctx, i18n.KeyAdminQueueLatestNone)},
			{"unreadable", View{LatestUnavailable: true}, i18n.T(ctx, i18n.KeyAdminQueueLatestUnavailable)},
			{"paid", View{Latest: &LatestPaid{Number: "GO-1", TotalCents: 128000}}, "GO-1 · NT$1,280"},
		} {
			if html := renderComponent(t, ctx, Dashboard(Meta(ctx), tc.view)); !strings.Contains(html, tc.want) {
				t.Errorf("%s in %v: the dashboard does not say %q", tc.name, loc, tc.want)
			}
		}
	}
}

// TestDashboardWeekUnavailable: the dashboard says when it could not read the week.
func TestDashboardWeekUnavailable(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	notice := i18n.T(ctx, i18n.KeyAdminQueueWeekUnavailable)
	for _, unavailable := range []bool{true, false} {
		html := renderComponent(t, ctx, Dashboard(Meta(ctx), View{WeekUnavailable: unavailable}))
		if got := strings.Contains(html, notice); got != unavailable {
			t.Errorf("Dashboard(WeekUnavailable=%v) shows the notice = %v", unavailable, got)
		}
		if !unavailable && !strings.Contains(html, i18n.Count(ctx, i18n.KeyAdminQueueRevenueDays, WeekDays, WeekDays)) {
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
		stat := widget.StatLine([]widget.Stat{{Label: "x", Value: elapsedValue(ctx, tc.d), LabelHref: "/"}}, widget.StatLineGlance)
		if html := renderComponent(t, ctx, stat); !strings.Contains(html, "<dd>"+tc.want) {
			t.Errorf("%s: elapsedValue(%v) renders %s, want <dd>%s", tc.name, tc.d, html, tc.want)
		}
	}
}

// TestWeek_AgainstReport: the week is compared as the report compares, and says
// that the week before is cut at the same time of day, because the whole days
// drawn above it do not add up to the figure.
func TestWeek_AgainstReport(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	// 44 against 31 orders is 13 apart, under two standard errors of 75.
	busy := busyWeek()
	if got, want := busy.ordersAgainst(ctx), i18n.Count(ctx, i18n.KeyAdminRepPreviousAtTime, 7, 7, "31"); got != want {
		t.Errorf("ordersAgainst inside the noise = %q, want %q", got, want)
	}
	big := Week{Orders: 300, RevenueCents: 3000000, RevenueSquares: 1, Previous: sales.Period{Orders: 100, RevenueCents: 1000000, RevenueSquares: 1}}
	if got, want := big.revenueAgainst(ctx), i18n.Count(ctx, i18n.KeyAdminRepMoreAtTime, 7, 7, 200); got != want {
		t.Errorf("revenueAgainst of a tripled week = %q, want %q", got, want)
	}
	noisy := Week{Orders: 30, RevenueCents: 3100000, RevenueSquares: 1e18, Previous: sales.Period{Orders: 30, RevenueCents: 3000000, RevenueSquares: 1e18}}
	if got, want := noisy.revenueAgainst(ctx), i18n.Count(ctx, i18n.KeyAdminRepPreviousAtTime, 7, 7, "NT$30,000"); got != want {
		t.Errorf("revenueAgainst inside the noise = %q, want %q", got, want)
	}
	if got := noisy.revenueAgainst(ctx); !strings.Contains(got, "同一時刻") {
		t.Errorf("revenueAgainst = %q, want it to say the week before is cut at the same time", got)
	}
}

// TestDashboardDaysCover: the dashboard shows the days cover in place of the low stock list.
// The dashboard's stock section is the days cover, not the low-stock table the
// sold-out task row already names.
func TestDashboardDaysCover(t *testing.T) {
	t.Parallel()

	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		render := func(v View) string { return renderComponent(t, ctx, Dashboard(Meta(ctx), v)) }

		t.Run(string(loc)+" sold out row opens the report", func(t *testing.T) {
			t.Parallel()
			view := View{SoldOut: 3}
			html := render(View{Tasks: view.DeskTasks(time.Now())})
			if !strings.Contains(html, `href="/admin/reports#stock"`) || !strings.Contains(html, i18n.T(ctx, i18n.KeyAdminQueueStatSoldOut)) {
				t.Error("the sold-out task row does not link to the report's stock section")
			}
			if strings.Contains(html, "/admin/stock?soldout=1") {
				t.Error("the dashboard still links to the low-stock list")
			}
		})

		t.Run(string(loc)+" rows of SKUs that are not sold out", func(t *testing.T) {
			t.Parallel()
			rows := []stockrisk.Row{
				{Risk: inventory.Risk{SKU: "FEW-1", Name: "Rare", Slug: "rare", Sales: inventory.Sales{Sellable: 4, Sold: 6, Orders: 3, InStock: stockedAllWindow}}},
				{Risk: inventory.Risk{SKU: "EST-1", Name: "Going", Slug: "going", Sales: inventory.Sales{Sellable: 20, Sold: 60, Orders: 30, InStock: stockedAllWindow}}},
			}
			html := render(View{Runway: rows})
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
			if rest := render(View{Runway: rows, RunwayCut: true}); !strings.Contains(rest, i18n.T(ctx, i18n.KeyAdminQueueRunwayRest)) {
				t.Error("rows were left off and the runway does not point to the report")
			}
		})

		t.Run(string(loc)+" nothing running out says so", func(t *testing.T) {
			t.Parallel()
			none := i18n.Count(ctx, i18n.KeyAdminQueueRunwayNone, inventory.CoverWarnDays, inventory.CoverWarnDays)
			unknown := i18n.T(ctx, i18n.KeyAdminQueueRunwayUnknown)
			noStock := i18n.Count(ctx, i18n.KeyAdminQueueRunwayNoStock, inventory.CoverWindowDays, inventory.CoverWindowDays)
			all := i18n.T(ctx, i18n.KeyAdminQueueRunwayAll)
			going := []stockrisk.Row{{Risk: inventory.Risk{SKU: "EST-1", Name: "Going", Slug: "going", Sales: inventory.Sales{Sellable: 9, Sold: 60, Orders: 30, InStock: stockedAllWindow}}}}

			for _, tc := range []struct {
				name string
				view View
				want []string
				not  []string
			}{
				{"estimated and none running out", View{SoldOut: 3, RunwayBasis: RunwayEstimated}, []string{none, all}, []string{unknown, noStock}},
				{"nothing can be estimated", View{SoldOut: 3}, []string{unknown, all}, []string{none, noStock}},
				{"everything is sold out", View{SoldOut: 3, RunwayBasis: RunwayNothingInStock}, []string{noStock, all}, []string{none, unknown}},
				{"rows listed", View{Runway: going, RunwayBasis: RunwayEstimated}, []string{all}, []string{none, unknown, i18n.T(ctx, i18n.KeyAdminQueueRunwayRest)}},
				{"rows left off", View{Runway: going, RunwayCut: true, RunwayBasis: RunwayEstimated}, []string{i18n.T(ctx, i18n.KeyAdminQueueRunwayRest)}, []string{none, unknown, all}},
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

			html := render(View{SoldOut: 3})
			for _, gone := range []string{"goen-report__rows", "goen-chartrangebar", `id="low-heading"`, "/admin/stock/", "/admin/stock?soldout=1"} {
				if strings.Contains(html, gone) {
					t.Errorf("the dashboard with nothing to estimate renders %q", gone)
				}
			}
		})
	}
}

// TestRunwayWithinLine: dashboard runway keeps only rows running out within the line.
func TestRunwayWithinLine(t *testing.T) {
	t.Parallel()

	// 9 sellable at 30 units over 30 days is 9 days; 45 sellable is 45 days.
	runningOut := func(n int) (rows []stockrisk.Row) {
		for i := range n {
			rows = append(rows, stockrisk.Row{Risk: inventory.Risk{SKU: fmt.Sprintf("RUN-%d", i), Sales: inventory.Sales{Sellable: 9, Sold: 30, Orders: 20, InStock: stockedAllWindow}}})
		}
		return rows
	}
	later := func(n int) (rows []stockrisk.Row) {
		for i := range n {
			rows = append(rows, stockrisk.Row{Risk: inventory.Risk{SKU: fmt.Sprintf("LATER-%d", i), Sales: inventory.Sales{Sellable: 45, Sold: 30, Orders: 20, InStock: stockedAllWindow}}})
		}
		return rows
	}
	soldOut := func(n int) (rows []stockrisk.Row) {
		for i := range n {
			rows = append(rows, stockrisk.Row{Risk: inventory.Risk{SKU: fmt.Sprintf("OUT-%d", i)}})
		}
		return rows
	}
	fewOrders := []stockrisk.Row{{Risk: inventory.Risk{SKU: "FEW-0", Sales: inventory.Sales{Sellable: 4, Sold: 6, Orders: 3, InStock: stockedAllWindow}}}}
	skus := func(rows []stockrisk.Row) (out []string) {
		for _, r := range rows {
			out = append(out, r.SKU)
		}
		return out
	}

	for _, tc := range []struct {
		name    string
		listed  []stockrisk.Row
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
		kept, cut, basis := Runway(tc.listed)
		if got := skus(kept); !slices.Equal(got, tc.want) || cut != tc.wantCut || basis != tc.wantBasis {
			t.Errorf("%s: Runway kept %v, cut %t, basis %d, want %v, cut %t, basis %d", tc.name, got, cut, basis, tc.want, tc.wantCut, tc.wantBasis)
		}
	}
}
