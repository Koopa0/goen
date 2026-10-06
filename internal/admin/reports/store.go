package reports

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/chart"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("reports: NewStore requires a pool")
	}
	return &Store{pool: pool}
}

var reportWindows = [...]int32{7, 30, 90}

const DefaultWindow int32 = 30

const maxRows = 10

// ErrDailyRevenue is returned with a complete report whose daily chart could
// not be read: the view is marked DailyUnavailable and is otherwise whole.
var ErrDailyRevenue = errors.New("read daily revenue")

// ReportAt reads the last days shop days up to now, and the same number before
// them, cut at the same time of day. The tiles and the daily chart are read in
// one snapshot, so the days add up to the revenue even while orders are paid.
func (s *Store) ReportAt(ctx context.Context, days int32, now time.Time) (admin.ReportView, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return admin.ReportView{}, fmt.Errorf("begin report snapshot: %w", err)
	}
	defer pgtx.Rollback(ctx, tx)
	q := db.New(tx)

	view, err := reportAt(ctx, q, days, now)
	if err != nil {
		return admin.ReportView{}, err
	}
	current, _ := periods(now, int(window(days)))
	view.Returned, view.ReturnedErr = returnedProducts(ctx, tx, current)
	// The chart is one figure of the page: failing to read it must not take the
	// tiles with it. It is read last, because a failed statement ends the snapshot.
	daily, err := dailyRevenue(ctx, q, days, now)
	if err != nil {
		view.DailyUnavailable = true
		return view, fmt.Errorf("%w: %w", ErrDailyRevenue, err)
	}
	view.Daily = daily
	return view, nil
}

func reportAt(ctx context.Context, q *db.Queries, days int32, now time.Time) (admin.ReportView, error) {
	days = window(days)
	current, before := periods(now, int(days))

	revenue, err := q.RevenueBetween(ctx, db.RevenueBetweenParams{FromAt: current.from, ToAt: current.to})
	if err != nil {
		return admin.ReportView{}, fmt.Errorf("read revenue: %w", err)
	}
	completion, err := q.CheckoutCompletionBetween(ctx, db.CheckoutCompletionBetweenParams{
		FromAt: current.from, ToAt: current.to,
	})
	if err != nil {
		return admin.ReportView{}, fmt.Errorf("read completion: %w", err)
	}
	prevRevenue, err := q.RevenueBetween(ctx, db.RevenueBetweenParams{FromAt: before.from, ToAt: before.to})
	if err != nil {
		return admin.ReportView{}, fmt.Errorf("read previous revenue: %w", err)
	}
	prevCompletion, err := q.CheckoutCompletionBetween(ctx, db.CheckoutCompletionBetweenParams{
		FromAt: before.from, ToAt: before.to,
	})
	if err != nil {
		return admin.ReportView{}, fmt.Errorf("read previous completion: %w", err)
	}
	sellers, err := q.BestSellersBetween(ctx, db.BestSellersBetweenParams{
		FromAt: current.from, ToAt: current.to, LimitTo: maxRows,
	})
	if err != nil {
		return admin.ReportView{}, fmt.Errorf("read best sellers: %w", err)
	}
	atRisk, moreSoldOut, err := stockAtRisk(ctx, q, int(days), now)
	if err != nil {
		return admin.ReportView{}, err
	}

	windows := make([]int32, len(reportWindows))
	copy(windows, reportWindows[:])
	view := admin.ReportView{
		Days:         int(days),
		Orders:       revenue.Orders,
		RevenueCents: revenue.RevenueCents, RefundedCents: revenue.RefundedCents,
		AverageCents: revenue.AverageCents, RevenueSquares: revenue.SumOfSquares,
		Placed:    completion.Placed,
		Committed: completion.Committed,
		Windows:   windows,
		AtRisk:    atRisk, MoreSoldOut: moreSoldOut, StockDays: max(int(days), admin.CoverWindowDays),
		From: shoptime.DateOf(current.from, now), To: shoptime.DateOf(current.to, now),
		Previous: admin.PreviousFigures{
			From: shoptime.DateOf(before.from, now), To: shoptime.DateOf(before.to, now),
			Orders: prevRevenue.Orders, RevenueCents: prevRevenue.RevenueCents,
			RevenueSquares: prevRevenue.SumOfSquares, RefundedCents: prevRevenue.RefundedCents,
			AverageCents: prevRevenue.AverageCents,
			Placed:       prevCompletion.Placed, Committed: prevCompletion.Committed,
		},
	}
	for i := range sellers {
		r := &sellers[i]
		view.Sellers = append(view.Sellers, admin.Seller{
			Slug: r.Slug, Name: r.Name, Brand: r.Brand,
			Units: r.Units, RevenueCents: r.RevenueCents,
		})
	}
	return view, nil
}

// returnedProducts reads inside a savepoint, so that a failure ends only the
// savepoint and not the snapshot the chart is still to be read from.
func returnedProducts(ctx context.Context, tx pgx.Tx, p period) ([]admin.ReturnedProduct, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin returned products savepoint: %w", err)
	}
	defer pgtx.Rollback(ctx, sp)
	rows, err := db.New(sp).ReturnedProductsBetween(ctx, db.ReturnedProductsBetweenParams{
		FromAt: p.from, ToAt: p.to, LimitTo: maxRows,
	})
	if err != nil {
		return nil, fmt.Errorf("read returned products: %w", err)
	}
	returned := make([]admin.ReturnedProduct, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		returned = append(returned, admin.ReturnedProduct{
			Slug: r.Slug, Name: r.Name, Brand: r.Brand,
			Returned: r.ReturnedUnits, Sold: r.SoldUnits,
		})
	}
	return returned, nil
}

// dailyRevenue reads each shop day's paid revenue over the same two periods
// reportAt reads, so the days add up to its revenue.
func dailyRevenue(ctx context.Context, q *db.Queries, days int32, now time.Time) (admin.DailyRevenue, error) {
	current, before := periods(now, int(window(days)))
	thisPeriod, err := dailySeries(ctx, q, current)
	if err != nil {
		return admin.DailyRevenue{}, err
	}
	previous, err := dailySeries(ctx, q, before)
	if err != nil {
		return admin.DailyRevenue{}, err
	}
	return admin.DailyRevenue{Current: thisPeriod, Previous: previous, Cut: shoptime.Clock(now)}, nil
}

func dailySeries(ctx context.Context, q *db.Queries, p period) (chart.Series, error) {
	rows, err := q.PaidByShopDay(ctx, db.PaidByShopDayParams{
		FirstDay: shopDate(p.from), LastDay: shopDate(p.to.Add(-time.Nanosecond)),
		FromAt: p.from, ToAt: p.to,
	})
	if err != nil {
		return chart.Series{}, fmt.Errorf("read paid revenue by day: %w", err)
	}
	series := chart.Series{Partial: endsMidDay(p), Buckets: make([]chart.Bucket, 0, len(rows))}
	for _, r := range rows {
		series.Buckets = append(series.Buckets, chart.Bucket{Day: r.Day, Value: r.RevenueCents})
	}
	return series, nil
}

// shopDate is the shop day t falls on, as the date a query takes.
func shopDate(t time.Time) time.Time {
	y, m, d := shoptime.In(t).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// endsMidDay is whether the period's last shop day is cut short.
func endsMidDay(p period) bool {
	return !p.to.Equal(shoptime.Midnight(p.to))
}

func window(days int32) int32 {
	if validWindow(days) {
		return days
	}
	return DefaultWindow
}

// validWindow is an allowlist, never a range: it reaches a scanning query.
func validWindow(days int32) bool {
	return slices.Contains(reportWindows[:], days)
}

// stockAtRisk ranks the SKUs that sold or are sold out over the last shop days,
// at least admin.CoverWindowDays of them whichever period the report shows.
func stockAtRisk(ctx context.Context, q *db.Queries, days int, now time.Time) ([]admin.StockRisk, int, error) {
	window, _ := periods(now, max(days, admin.CoverWindowDays))
	rows, err := q.StockAtRisk(ctx, db.StockAtRiskParams{FromAt: window.from, ToAt: window.to})
	if err != nil {
		return nil, 0, fmt.Errorf("read stock at risk: %w", err)
	}
	ids := make([]uuid.UUID, len(rows))
	for i := range rows {
		ids[i] = rows[i].VariantID
	}
	moves := map[uuid.UUID][]movement{}
	if len(ids) > 0 {
		ledger, err := q.StockMovementsSince(ctx, db.StockMovementsSinceParams{VariantIds: ids, FromAt: window.from})
		if err != nil {
			return nil, 0, fmt.Errorf("read stock movements: %w", err)
		}
		for i := range ledger {
			m := &ledger[i]
			moves[m.VariantID] = append(moves[m.VariantID], movement{at: m.CreatedAt, delta: m.Delta})
		}
	}
	risk := make([]admin.StockRisk, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		risk = append(risk, admin.StockRisk{
			SKU: r.SKU, Name: r.ProductName, Slug: r.Slug,
			Sellable: max(r.StockQuantity-r.SafetyStock, 0),
			Sold:     r.UnitsSold, Orders: r.OrdersSold,
			InStock:   timeInStock(r.StockQuantity, r.SafetyStock, window.from, window.to, moves[r.VariantID]),
			SoldOutAt: soldOutAt(r.StockQuantity, r.SafetyStock, window.to, moves[r.VariantID]),
		})
	}
	listed, more := admin.RankStockRisk(risk)
	return listed, more, nil
}
