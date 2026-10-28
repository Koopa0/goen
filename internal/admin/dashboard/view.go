package dashboard

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/product/inventory"
	"github.com/koopa0/goen/internal/sales"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/chart"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/stockrisk"
	"github.com/koopa0/goen/internal/ui/widget"
)

// View lists the newest orders in Recent whatever their state: filtering it
// to one state would answer what the tiles above already answer, and hide the order
// somebody walked over to ask about.
type View struct {
	PendingOrders int64
	ReadyOrders   int64
	// ReadyOldestSince and the other *OldestSince are when the oldest waiting
	// item began to wait; with nothing waiting they are the time it was read.
	ReadyOldestSince        time.Time
	PickingOrders           int64
	PickingOldestSince      time.Time
	SoldOut                 int64
	OpenMessages            int64
	OpenMessagesOldestSince time.Time
	// PendingReturns is the requests nobody has decided, UninspectedReturns the
	// approved ones whose parcel nobody has opened.
	PendingReturns                 int64
	PendingReturnsOldestSince      time.Time
	UninspectedReturns             int64
	UninspectedReturnsOldestSince  time.Time
	UnansweredQuestions            int64
	UnansweredQuestionsOldestSince time.Time
	Recent                         []OrderRow
	// Runway is the first days cover rows of SKUs expected to run out within
	// the warning line, RunwayCut says more were left off and RunwayBasis what
	// an empty list can say.
	Runway      []stockrisk.Row
	RunwayCut   bool
	RunwayBasis RunwayBasis
	// Tasks is the work that waits for a person, in the order it is listed.
	Tasks []Task
	// HealthUnavailable is set when the health desk could not be read, so an
	// absent payment or invoice task is not taken for "nothing to check".
	HealthUnavailable bool
	// Week is the last seven days; WeekUnavailable says it could not be read,
	// which is not the same as a week without orders. Likewise Latest, nil when
	// no order was ever paid, and LatestUnavailable.
	Week              Week
	WeekUnavailable   bool
	Latest            *LatestPaid
	LatestUnavailable bool
	// Daily is the revenue of every shop day of the last DailyDays, today cut
	// at ReadAt; Sellers are the products sold most over the last WeekDays.
	Daily   chart.Series
	ReadAt  time.Time
	Sellers []sales.Seller
}

// OrderRow is one of the newest orders as the dashboard lists it. It is a copy
// of the orders queue's row (orders.Row): the two pages show the same
// order the same way. TestStore_RecentOrdersMatchTheQueue converts one into
// the other, so their fields cannot drift apart.
type OrderRow struct {
	Number       string
	Status       order.FulfillmentStatus
	StatusText   string
	StatusIntent widget.Intent
	PlacedAt     string
	Recipient    string
	TotalCents   int64
}

func (o OrderRow) Total() string { return money.TWD(o.TotalCents) }

func (o OrderRow) RecipientText(ctx context.Context) string {
	if o.Recipient == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedRecipient)
	}
	return o.Recipient
}

// Task is one kind of work that waits for a person: what it is, how much of
// it, and where it is done.
type Task struct {
	Label i18n.Key
	Count int64
	Href  string
	// HasAge is false for work with no start time to measure from.
	HasAge bool
	// WaitDays is the shop days the oldest item has waited, counted as the
	// orders desk's work tabs count them; Overdue is set on the order tasks once
	// that is more than order.PackWithinDays.
	WaitDays int
	Overdue  bool
	// Alert marks work that is wrong rather than merely waiting.
	Alert bool
}

// waiting is a task whose oldest item began to wait at since, counted in shop
// days at now.
func waiting(label i18n.Key, count int64, href string, since, now time.Time) Task {
	return Task{Label: label, Count: count, Href: href, HasAge: true, WaitDays: shoptime.DaysAgo(since, now)}
}

// packing is a waiting order task, which is overdue once it has waited longer
// than order.PackWithinDays.
func packing(label i18n.Key, count int64, href string, since, now time.Time) Task {
	t := waiting(label, count, href, since, now)
	t.Overdue = order.PackingOverdue(t.WaitDays)
	return t
}

// AgeText says how long the oldest item has waited, in whole shop days.
func (t Task) AgeText(ctx context.Context) string {
	if t.WaitDays < 1 {
		return i18n.T(ctx, i18n.KeyAdminQueueTaskToday)
	}
	return i18n.Count(ctx, i18n.KeyAdminQueueTaskOldestDays, int64(t.WaitDays), t.WaitDays)
}

// DeskTasks lists what the order desk itself counts, leaving out each kind with
// nothing waiting, with each wait counted in shop days at now.
func (v *View) DeskTasks(now time.Time) []Task {
	all := []Task{
		packing(i18n.KeyAdminStatusReadyToPick, v.ReadyOrders, "/admin/orders?status=ready", v.ReadyOldestSince, now),
		packing(i18n.KeyAdminStatusPicking, v.PickingOrders, "/admin/orders?status=picking", v.PickingOldestSince, now),
		{Label: i18n.KeyAdminQueueStatPending, Count: v.PendingOrders, Href: "/admin/orders?status=pending"},
		waiting(i18n.KeyAdminQueueStatReturns, v.PendingReturns, "/admin/returns", v.PendingReturnsOldestSince, now),
		waiting(i18n.KeyAdminQueueTaskUninspected, v.UninspectedReturns, "/admin/returns", v.UninspectedReturnsOldestSince, now),
		waiting(i18n.KeyAdminQueueStatQuestions, v.UnansweredQuestions, "/admin/questions", v.UnansweredQuestionsOldestSince, now),
		waiting(i18n.KeyAdminQueueStatMessages, v.OpenMessages, "/admin/messages", v.OpenMessagesOldestSince, now),
		{Label: i18n.KeyAdminQueueStatSoldOut, Count: v.SoldOut, Href: "/admin/reports#stock"},
	}
	return slices.DeleteFunc(all, func(t Task) bool { return t.Count == 0 })
}

// SalesChart is the revenue of the last DailyDays: the days before the last
// WeekDays in the previous colour, named by their dates, today open.
func (v *View) SalesChart(ctx context.Context) chart.ColumnsProps {
	days := v.Daily
	days.Label = i18n.T(ctx, i18n.KeyAdminRepRevenue)
	return chart.ColumnsProps{
		Series: days, Measure: chart.MeasureMoney,
		Previous: WeekDays, PreviousLabel: i18n.Count(ctx, i18n.KeyAdminRepPreviousDays, WeekDays, WeekDays),
		Caption:      v.salesCaption(ctx),
		Note:         fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepPaidNote), shoptime.Clock(v.ReadAt)),
		DayHeading:   i18n.T(ctx, i18n.KeyAdminRepDate),
		SpanHeading:  i18n.T(ctx, i18n.KeyChartPeriod),
		PartialLabel: fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepUntil), shoptime.Clock(v.ReadAt)),
	}
}

// salesCaption names the earlier stretch by its dates and the highest day, so
// that neither is told by colour alone.
func (v *View) salesCaption(ctx context.Context) string {
	buckets := v.Daily.Buckets
	peaks := chart.Peaks(v.Daily.Columns())
	if len(buckets) < WeekDays || len(peaks) == 0 {
		return ""
	}
	label := func(day time.Time) string { return chart.DayLabel(ctx, shoptime.ColumnDate(day)) }
	earlier := label(buckets[0].Day) + "–" + label(buckets[WeekDays-1].Day)
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminQueueSalesCaption), WeekDays, earlier, label(peaks[0].Day), money.TWD(peaks[0].Value))
}

// SalesSentence is what the revenue section says in place of the chart, when
// too few of the days had a paid order to draw one.
func (v *View) SalesSentence(ctx context.Context) string {
	filled := 0
	for _, b := range v.Daily.Buckets {
		if b.Value != 0 {
			filled++
		}
	}
	if filled == 0 {
		return i18n.Count(ctx, i18n.KeyAdminQueueSalesNone, DailyDays, DailyDays)
	}
	return i18n.Count(ctx, i18n.KeyAdminQueueSalesFew, int64(filled), filled, DailyDays)
}

func Meta(ctx context.Context) frame.Page {
	return frame.Page{Title: i18n.T(ctx, i18n.KeyAdminPageDashboard)}
}

// dashboardRunwayRows is how many of the days cover rows the dashboard lists.
const dashboardRunwayRows = 5

// RunwayBasis says what an empty dashboard runway can truthfully say.
type RunwayBasis uint8

const (
	// RunwayTooFewSales: no SKU in stock could be estimated yet.
	RunwayTooFewSales RunwayBasis = iota
	// RunwayEstimated: some SKU was estimated.
	RunwayEstimated
	// RunwayNothingInStock: every SKU that sold in the window is sold out.
	RunwayNothingInStock
)

// Runway keeps the first rows of a ranked days cover list that run out
// within the warning line: the sold-out task row already counts the sold out
// ones and links to them, and an estimate past the line is not what the
// section's title promises. cut reports that rows were left off; basis tells an
// empty list apart: nothing is running out, nothing can be estimated yet, or
// nothing that sold in the window is left in stock.
func Runway(listed []stockrisk.Row) (kept []stockrisk.Row, cut bool, basis RunwayBasis) {
	inStock := false
	for i := range listed {
		cover := listed[i].Estimate()
		if cover.State != inventory.CoverSoldOut {
			inStock = true
		}
		if cover.State != inventory.CoverEstimated {
			continue
		}
		basis = RunwayEstimated
		if !cover.Urgent() {
			continue
		}
		if len(kept) == dashboardRunwayRows {
			return kept, true, basis
		}
		kept = append(kept, listed[i])
	}
	if basis == RunwayTooFewSales && len(listed) > 0 && !inStock {
		basis = RunwayNothingInStock
	}
	return kept, false, basis
}
