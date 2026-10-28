package reports

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/product/inventory"
	"github.com/koopa0/goen/internal/sales"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/chart"
	"github.com/koopa0/goen/internal/ui/stockrisk"
)

// Cover ranks the SKUs by days cover over the last shop days, for the report's
// stock section; the stock desk implements it.
type Cover interface {
	DaysCover(ctx context.Context, days int, now time.Time) (listed []inventory.Risk, moreSoldOut int, err error)
}

type Store struct {
	pool  *pgxpool.Pool
	cover Cover
}

func NewStore(pool *pgxpool.Pool, cover Cover) *Store {
	if pool == nil {
		panic("reports: NewStore requires a pool")
	}
	if cover == nil {
		panic("reports: NewStore requires a cover")
	}
	return &Store{pool: pool, cover: cover}
}

var reportWindows = [...]int32{7, 30, 90}

const DefaultWindow int32 = 30

const maxRows = 10

// ErrDailyRevenue is returned with a complete report whose daily charts could
// not be read: the view is marked DailyUnavailable and is otherwise whole. The
// charts share their reads, the campaigns and the latest paid day among them,
// so one failing leaves both without their drawing.
var ErrDailyRevenue = errors.New("read daily revenue")

// ReportAt reads the last days shop days up to now, and the same number before
// them, cut at the same time of day. The tiles and the daily chart are read in
// one snapshot, so the days add up to the revenue even while orders are paid.
func (s *Store) ReportAt(ctx context.Context, days int32, now time.Time) (View, error) {
	// The stock and its ledger are read in their own snapshot, before this one
	// opens, so one report holds one connection at a time.
	atRisk, moreSoldOut, err := s.cover.DaysCover(ctx, int(window(days)), now)
	if err != nil {
		return View{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return View{}, fmt.Errorf("begin report snapshot: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := db.New(tx)

	view, err := reportAt(ctx, q, days, now)
	if err != nil {
		return View{}, err
	}
	view.AtRisk, view.MoreSoldOut = stockrisk.NewRows(atRisk, now), moreSoldOut
	current, _ := periods(now, int(window(days)))
	view.Returned, view.ReturnedErr = readReturnedProducts(ctx, tx, current)
	// The daily charts are figures of the page: failing to read them must not take
	// the tiles with them. It is read last, because a failed statement ends the snapshot.
	daily, paid, err := dailyFigures(ctx, q, days, now)
	if err != nil {
		view.DailyUnavailable = true
		return view, fmt.Errorf("%w: %w", ErrDailyRevenue, err)
	}
	view.Daily, view.Paid = daily, paid
	return view, nil
}

// Recent reads paid orders and revenue over a window of days shop days up to
// now and over the window before it, cut at the same time of day, in one
// snapshot so the days add up to the totals. The same snapshot reads every
// shop day the two windows span, each whole but today, which ends at now, and
// the current window's best sellers. The latest paid order is read last:
// failing to read it leaves the rest standing, returned with
// sales.ErrLatestPaid.
func (s *Store) Recent(ctx context.Context, days int, now time.Time) (sales.Recent, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return sales.Recent{}, fmt.Errorf("begin week snapshot: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := db.New(tx)

	current, before := periods(now, days)
	var recent sales.Recent
	if recent.Current, err = readPeriod(ctx, q, current); err != nil {
		return sales.Recent{}, err
	}
	if recent.Previous, err = readPeriod(ctx, q, before); err != nil {
		return sales.Recent{}, err
	}
	if recent.Days, err = readWholeDays(ctx, q, now, days); err != nil {
		return sales.Recent{}, err
	}
	if recent.BestSellers, err = readBestSellers(ctx, q, current, sales.RecentBestSellers); err != nil {
		return sales.Recent{}, err
	}

	latest, err := latestPaidOrder(ctx, q, before.from)
	switch {
	case err == nil:
		recent.Latest = &sales.PaidOrder{
			Number: latest.OrderNumber, TotalCents: latest.TotalCents, Elapsed: time.Duration(latest.ElapsedSeconds) * time.Second,
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return recent, fmt.Errorf("%w: %w", sales.ErrLatestPaid, err)
	}
	return recent, nil
}

// latestPaidOrder looks among the orders placed since the earlier of the two
// periods already read, and only when none of them was paid among all of them.
func latestPaidOrder(ctx context.Context, q *db.Queries, since time.Time) (db.LatestPaidOrderRow, error) {
	latest, err := q.LatestPaidOrder(ctx, since)
	if errors.Is(err, pgx.ErrNoRows) {
		return q.LatestPaidOrder(ctx, time.Time{})
	}
	return latest, err
}

// readPeriod is the period's paid orders and revenue.
func readPeriod(ctx context.Context, q *db.Queries, p period) (sales.Period, error) {
	revenue, err := q.RevenueBetween(ctx, db.RevenueBetweenParams{FromAt: p.from, ToAt: p.to})
	if err != nil {
		return sales.Period{}, fmt.Errorf("read the week's revenue: %w", err)
	}
	return sales.Period{
		Orders: revenue.Orders, RevenueCents: revenue.RevenueCents, RevenueSquares: revenue.SumOfSquares,
		Partial: endsMidDay(p),
	}, nil
}

// wholeDays is the shop days a [sales.Recent] lays out: the last 2*days of
// them, today included. The orders are bounded by span, which runs from the
// first day's midnight to now; first and last are the days the rows cover, and
// the last is today even when now is its first instant.
func wholeDays(now time.Time, days int) (span period, first, last time.Time) {
	span = period{from: shoptime.FirstDay(now, 2*days), to: now}
	return span, shoptime.QueryDate(span.from), shoptime.QueryDate(now)
}

// readWholeDays is each of the last 2*days shop days' paid orders and revenue,
// a day without orders included. Reading the two periods apart and joining
// them would lose the hours between the earlier one's cut and the day after.
func readWholeDays(ctx context.Context, q *db.Queries, now time.Time, days int) ([]sales.Day, error) {
	span, first, last := wholeDays(now, days)
	rows, err := q.PaidByShopDay(ctx, db.PaidByShopDayParams{
		FirstDay: first, LastDay: last, FromAt: span.from, ToAt: span.to,
	})
	if err != nil {
		return nil, fmt.Errorf("read the paid orders of whole days: %w", err)
	}
	whole := make([]sales.Day, 0, len(rows))
	for _, r := range rows {
		whole = append(whole, sales.Day{Day: r.Day, Orders: r.Orders, RevenueCents: r.RevenueCents})
	}
	return whole, nil
}

// readBestSellers is the period's best sellers, at most limit of them.
func readBestSellers(ctx context.Context, q *db.Queries, p period, limit int32) ([]sales.BestSeller, error) {
	rows, err := q.BestSellersBetween(ctx, db.BestSellersBetweenParams{FromAt: p.from, ToAt: p.to, LimitTo: limit})
	if err != nil {
		return nil, fmt.Errorf("read best sellers: %w", err)
	}
	sellers := make([]sales.BestSeller, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		sellers = append(sellers, sales.BestSeller{
			Slug: r.Slug, Name: r.Name, Brand: r.Brand, Units: r.Units, RevenueCents: r.RevenueCents,
		})
	}
	return sellers, nil
}

func reportAt(ctx context.Context, q *db.Queries, days int32, now time.Time) (View, error) {
	days = window(days)
	current, before := periods(now, int(days))

	revenue, err := q.RevenueBetween(ctx, db.RevenueBetweenParams{FromAt: current.from, ToAt: current.to})
	if err != nil {
		return View{}, fmt.Errorf("read revenue: %w", err)
	}
	completion, err := q.CheckoutCompletionBetween(ctx, db.CheckoutCompletionBetweenParams{
		FromAt: current.from, ToAt: current.to,
	})
	if err != nil {
		return View{}, fmt.Errorf("read completion: %w", err)
	}
	prevRevenue, err := q.RevenueBetween(ctx, db.RevenueBetweenParams{FromAt: before.from, ToAt: before.to})
	if err != nil {
		return View{}, fmt.Errorf("read previous revenue: %w", err)
	}
	prevCompletion, err := q.CheckoutCompletionBetween(ctx, db.CheckoutCompletionBetweenParams{
		FromAt: before.from, ToAt: before.to,
	})
	if err != nil {
		return View{}, fmt.Errorf("read previous completion: %w", err)
	}
	sellers, err := readBestSellers(ctx, q, current, maxRows)
	if err != nil {
		return View{}, err
	}
	departments, err := q.DepartmentSalesBetween(ctx, db.DepartmentSalesBetweenParams{
		Locale: string(i18n.FromContext(ctx)), FromAt: current.from, ToAt: current.to,
	})
	if err != nil {
		return View{}, fmt.Errorf("read department sales: %w", err)
	}

	windows := make([]int32, len(reportWindows))
	copy(windows, reportWindows[:])
	view := View{
		Days:         int(days),
		Orders:       revenue.Orders,
		RevenueCents: revenue.RevenueCents, RefundedCents: revenue.RefundedCents,
		AverageCents: revenue.AverageCents, RevenueSquares: revenue.SumOfSquares,
		Placed:    completion.Placed,
		Committed: completion.Committed,
		Windows:   windows,
		StockDays: max(int(days), inventory.CoverWindowDays),
		From:      shoptime.DateOf(current.from, now), To: shoptime.DateOf(current.to, now),
		Previous: PreviousFigures{
			From: shoptime.DateOf(before.from, now), To: shoptime.DateOf(before.to, now),
			Orders: prevRevenue.Orders, RevenueCents: prevRevenue.RevenueCents,
			RevenueSquares: prevRevenue.SumOfSquares, RefundedCents: prevRevenue.RefundedCents,
			AverageCents: prevRevenue.AverageCents,
			Placed:       prevCompletion.Placed, Committed: prevCompletion.Committed,
		},
	}
	view.BestSellers = sellers
	for _, d := range departments {
		view.Departments = append(view.Departments, Department{Name: d.Name, SalesCents: d.SalesCents})
	}
	return view, nil
}

// readReturnedProducts reads inside a savepoint, so that a failure ends only
// the savepoint and not the snapshot the chart is still to be read from.
func readReturnedProducts(ctx context.Context, tx pgx.Tx, p period) ([]ReturnedProduct, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin returned products savepoint: %w", err)
	}
	defer db.Rollback(ctx, sp)
	rows, err := db.New(sp).ReturnedProductsBetween(ctx, db.ReturnedProductsBetweenParams{
		FromAt: p.from, ToAt: p.to, LimitTo: maxRows,
	})
	if err != nil {
		return nil, fmt.Errorf("read returned products: %w", err)
	}
	returned := make([]ReturnedProduct, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		returned = append(returned, ReturnedProduct{
			Slug: r.Slug, Name: r.Name, Brand: r.Brand,
			Returned: r.ReturnedUnits, Sold: r.SoldUnits,
		})
	}
	return returned, nil
}

// dailyFigures reads each shop day's paid revenue over the same two periods
// reportAt reads, so the days add up to its revenue, and the paid orders of the
// current period's days with what was on during them.
func dailyFigures(ctx context.Context, q *db.Queries, days int32, now time.Time) (DailyRevenue, PaidDays, error) {
	current, before := periods(now, int(window(days)))
	thisPeriod, orders, err := dailySeries(ctx, q, current)
	if err != nil {
		return DailyRevenue{}, PaidDays{}, err
	}
	previous, _, err := dailySeries(ctx, q, before)
	if err != nil {
		return DailyRevenue{}, PaidDays{}, err
	}
	paid := PaidDays{Days: orders}
	campaigns, err := q.CampaignsBetween(ctx, db.CampaignsBetweenParams{
		Locale: string(i18n.FromContext(ctx)), FromAt: current.from, ToAt: current.to,
	})
	if err != nil {
		return DailyRevenue{}, PaidDays{}, fmt.Errorf("read campaigns: %w", err)
	}
	for _, c := range campaigns {
		paid.Campaigns = append(paid.Campaigns, chart.Span{From: c.FirstDay, To: c.LastDay, Label: c.Title})
	}
	if orders.Density() == chart.DensityNone {
		// The latest order is read only to say when it was: its absence is a
		// shop that never sold, not a failure.
		latest, err := q.LatestPaidDay(ctx, current.to)
		switch {
		case err == nil:
			day := shoptime.DateOf(latest, now)
			paid.Latest = &day
		case !errors.Is(err, pgx.ErrNoRows):
			return DailyRevenue{}, PaidDays{}, fmt.Errorf("read latest paid day: %w", err)
		}
	}
	return DailyRevenue{Current: thisPeriod, Previous: previous, Cut: shoptime.Clock(now)}, paid, nil
}

// dailySeries is the period's paid revenue and its paid orders, a bucket for
// each shop day.
func dailySeries(ctx context.Context, q *db.Queries, p period) (revenue, orders chart.Series, err error) {
	rows, err := q.PaidByShopDay(ctx, db.PaidByShopDayParams{
		FirstDay: shoptime.QueryDate(p.from), LastDay: shoptime.QueryDate(p.to.Add(-time.Nanosecond)),
		FromAt: p.from, ToAt: p.to,
	})
	if err != nil {
		return chart.Series{}, chart.Series{}, fmt.Errorf("read paid revenue by day: %w", err)
	}
	revenue = chart.Series{Partial: endsMidDay(p), Buckets: make([]chart.Bucket, 0, len(rows))}
	orders = chart.Series{Partial: revenue.Partial, Buckets: make([]chart.Bucket, 0, len(rows))}
	for _, r := range rows {
		revenue.Buckets = append(revenue.Buckets, chart.Bucket{Day: r.Day, Value: r.RevenueCents})
		orders.Buckets = append(orders.Buckets, chart.Bucket{Day: r.Day, Value: r.Orders})
	}
	return revenue, orders, nil
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
