package dashboard

import (
	"context"
	"slices"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/inventory"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

// DashboardView lists the newest orders in Recent whatever their state: filtering it
// to one state would answer what the tiles above already answer, and hide the order
// somebody walked over to ask about.
type DashboardView struct {
	PendingOrders int64
	ReadyOrders   int64
	// ReadyOldestSeconds and the other *OldestSeconds are how long the oldest
	// waiting item has waited, on the database's clock.
	ReadyOldestSeconds        int64
	PickingOrders             int64
	PickingOldestSeconds      int64
	SoldOut                   int64
	OpenMessages              int64
	OpenMessagesOldestSeconds int64
	// PendingReturns is the requests nobody has decided, UninspectedReturns the
	// approved ones whose parcel nobody has opened.
	PendingReturns                   int64
	PendingReturnsOldestSeconds      int64
	UninspectedReturns               int64
	UninspectedReturnsOldestSeconds  int64
	UnansweredQuestions              int64
	UnansweredQuestionsOldestSeconds int64
	Recent                           []OrderRow
	// Runway is the first days cover rows of SKUs expected to run out within
	// the warning line, RunwayCut says more were left off and RunwayBasis what
	// an empty list can say.
	Runway      []admin.StockRisk
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
}

// OrderRow is one of the newest orders as the dashboard lists it. It is a copy
// of the orders queue's row (orders.OrderRow): the two pages show the same
// order the same way. TestStore_RecentOrdersMatchTheQueue converts one into
// the other, so their fields cannot drift apart.
type OrderRow struct {
	Number       string
	Status       order.FulfillmentStatus
	StatusText   string
	StatusIntent components.Intent
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
	HasAge     bool
	AgeSeconds int64
	// Alert marks work that is wrong rather than merely waiting.
	Alert bool
}

const secondsPerDay = 24 * 60 * 60

// AgeText says how long the oldest item has waited, in whole days.
func (t Task) AgeText(ctx context.Context) string {
	days := t.AgeSeconds / secondsPerDay
	if days < 1 {
		return i18n.T(ctx, i18n.KeyAdminQueueTaskUnderADay)
	}
	return i18n.Count(ctx, i18n.KeyAdminQueueTaskOldestDays, days, days)
}

// DeskTasks lists what the order desk itself counts, leaving out each kind with
// nothing waiting.
func (v *DashboardView) DeskTasks() []Task {
	all := []Task{
		{Label: i18n.KeyAdminStatusReadyToPick, Count: v.ReadyOrders, Href: "/admin/orders?status=ready", HasAge: true, AgeSeconds: v.ReadyOldestSeconds},
		{Label: i18n.KeyAdminStatusPicking, Count: v.PickingOrders, Href: "/admin/orders?status=picking", HasAge: true, AgeSeconds: v.PickingOldestSeconds},
		{Label: i18n.KeyAdminQueueStatPending, Count: v.PendingOrders, Href: "/admin/orders?status=pending"},
		{Label: i18n.KeyAdminQueueStatReturns, Count: v.PendingReturns, Href: "/admin/returns", HasAge: true, AgeSeconds: v.PendingReturnsOldestSeconds},
		{Label: i18n.KeyAdminQueueTaskUninspected, Count: v.UninspectedReturns, Href: "/admin/returns", HasAge: true, AgeSeconds: v.UninspectedReturnsOldestSeconds},
		{Label: i18n.KeyAdminQueueStatQuestions, Count: v.UnansweredQuestions, Href: "/admin/questions", HasAge: true, AgeSeconds: v.UnansweredQuestionsOldestSeconds},
		{Label: i18n.KeyAdminQueueStatMessages, Count: v.OpenMessages, Href: "/admin/messages", HasAge: true, AgeSeconds: v.OpenMessagesOldestSeconds},
		{Label: i18n.KeyAdminQueueStatSoldOut, Count: v.SoldOut, Href: "/admin/reports#stock"},
	}
	return slices.DeleteFunc(all, func(t Task) bool { return t.Count == 0 })
}

func Meta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyAdminPageDashboard)}
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

// DashboardRunway keeps the first rows of a ranked days cover list that run out
// within the warning line: the sold-out task row already counts the sold out
// ones and links to them, and an estimate past the line is not what the
// section's title promises. cut reports that rows were left off; basis tells an
// empty list apart: nothing is running out, nothing can be estimated yet, or
// nothing that sold in the window is left in stock.
func DashboardRunway(listed []admin.StockRisk) (kept []admin.StockRisk, cut bool, basis RunwayBasis) {
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
