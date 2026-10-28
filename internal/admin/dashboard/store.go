// Package dashboard is the back office's landing page, GET /admin: what waits
// for a person at every desk. It reads its counts and the newest orders with
// queries of its own, and the stock runway, the health tasks and the week's
// sales through the interfaces below, which the stock, health and reports desks
// satisfy.
package dashboard

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/worklist"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/product/inventory"
	"github.com/koopa0/goen/internal/sales"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/orderstatus"
	"github.com/koopa0/goen/internal/ui/stockrisk"
)

// Stock ranks the SKUs by days cover; the stock desk implements it.
type Stock interface {
	DaysCover(ctx context.Context, days int, now time.Time) (listed []inventory.Risk, moreSoldOut int, err error)
}

// Health lists what the health desk judges to need a person; the health desk
// implements it.
type Health interface {
	Tasks(ctx context.Context) ([]worklist.Task, error)
}

// Sales reads paid orders and revenue over a window of shop days up to now and
// over the window before it, and the latest paid order; the reports desk
// implements it.
type Sales interface {
	Recent(ctx context.Context, days int, now time.Time) (sales.Recent, error)
}

type Store struct {
	q      *db.Queries
	stock  Stock
	health Health
	sales  Sales
}

func NewStore(pool *pgxpool.Pool, stock Stock, health Health, sold Sales) *Store {
	if pool == nil || stock == nil || health == nil || sold == nil {
		panic("dashboard: NewStore requires a pool, stock, health and sales")
	}
	return &Store{q: db.New(pool), stock: stock, health: health, sales: sold}
}

func (s *Store) Dashboard(ctx context.Context) (View, error) {
	sum, err := s.q.AdminSummary(ctx)
	if err != nil {
		return View{}, fmt.Errorf("read summary: %w", err)
	}
	view := View{
		PendingOrders:           sum.PendingOrders,
		ReadyOrders:             sum.ReadyOrders,
		ReadyOldestSince:        sum.ReadyOldestSince,
		PickingOrders:           sum.PickingOrders,
		PickingOldestSince:      sum.PickingOldestSince,
		SoldOut:                 sum.SoldOut,
		OpenMessages:            sum.OpenMessages,
		OpenMessagesOldestSince: sum.OpenMessagesOldestSince,

		PendingReturns:                 sum.PendingReturns,
		PendingReturnsOldestSince:      sum.PendingReturnsOldestSince,
		UninspectedReturns:             sum.UninspectedReturns,
		UninspectedReturnsOldestSince:  sum.UninspectedReturnsOldestSince,
		UnansweredQuestions:            sum.UnansweredQuestions,
		UnansweredQuestionsOldestSince: sum.UnansweredQuestionsOldestSince,
	}
	// No status: the newest orders whatever state they are in. The tiles above
	// the queue already count each state, and a queue filtered to one of them
	// hides the order somebody is standing at the counter asking about.
	recent, err := s.q.RecentOrders(ctx, recentRows)
	if err != nil {
		return View{}, fmt.Errorf("read recent orders: %w", err)
	}
	rows := make([]OrderRow, len(recent))
	for i := range recent {
		rows[i] = orderRow(ctx, &recent[i])
	}
	view.Recent = rows

	now := time.Now()
	listed, _, err := s.stock.DaysCover(ctx, inventory.CoverWindowDays, now)
	if err != nil {
		return View{}, fmt.Errorf("read days cover: %w", err)
	}
	view.Runway, view.RunwayCut, view.RunwayBasis = Runway(stockrisk.NewRows(listed, now))
	view.Tasks = view.DeskTasks(now)
	return view, nil
}

// HealthTasks is what the health desk says needs a person. The dashboard is
// still worth opening without it, so the caller decides what an error costs.
func (s *Store) HealthTasks(ctx context.Context) ([]worklist.Task, error) {
	return s.health.Tasks(ctx)
}

// RecentSales is the shop's sales over a window of days shop days up to now and
// the window before it. The dashboard is still worth opening without them, so
// the caller decides what an error costs.
func (s *Store) RecentSales(ctx context.Context, days int, now time.Time) (sales.Recent, error) {
	return s.sales.Recent(ctx, days, now)
}

// recentRows is how many of the newest orders the landing page lists.
const recentRows = 8

// orderRow is one order as the landing page lists it. It reads the same word,
// colour and total as the orders queue's row for the same order; the total is
// read from order_totals, as on every page that states one.
func orderRow(ctx context.Context, o *db.RecentOrdersRow) OrderRow {
	fulfillment := order.FulfillmentStatus(o.FulfillmentStatus)
	text, intent := orderstatus.Badge(ctx, fulfillment, o.Committed, o.OwedCents, o.Returned)
	return OrderRow{
		Number:       o.OrderNumber,
		Status:       fulfillment,
		StatusText:   text,
		StatusIntent: intent,
		PlacedAt:     shoptime.Minute(o.PlacedAt),
		Recipient:    o.Recipient,
		TotalCents:   o.TotalCents,
	}
}
