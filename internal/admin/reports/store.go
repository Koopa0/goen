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
	"github.com/koopa0/goen/internal/i18n"
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

// ErrDailyRevenue is returned with a complete report whose daily charts could
// not be read: the view is marked DailyUnavailable and is otherwise whole. The
// charts share their reads, the campaigns and the latest paid day among them,
// so one failing leaves both without their drawing.
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

// dailyFigures reads each shop day's paid revenue over the same two periods
// reportAt reads, so the days add up to its revenue, and the paid orders of the
// current period's days with what was on during them.
func dailyFigures(ctx context.Context, q *db.Queries, days int32, now time.Time) (admin.DailyRevenue, admin.PaidDays, error) {
	current, before := periods(now, int(window(days)))
	thisPeriod, orders, err := dailySeries(ctx, q, current)
	if err != nil {
		return admin.DailyRevenue{}, admin.PaidDays{}, err
	}
	previous, _, err := dailySeries(ctx, q, before)
	if err != nil {
		return admin.DailyRevenue{}, admin.PaidDays{}, err
	}
	paid := admin.PaidDays{Days: orders}
	campaigns, err := q.CampaignsBetween(ctx, db.CampaignsBetweenParams{
		Locale: string(i18n.FromContext(ctx)), FromAt: current.from, ToAt: current.to,
	})
	if err != nil {
		return admin.DailyRevenue{}, admin.PaidDays{}, fmt.Errorf("read campaigns: %w", err)
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
			return admin.DailyRevenue{}, admin.PaidDays{}, fmt.Errorf("read latest paid day: %w", err)
		}
	}
	return admin.DailyRevenue{Current: thisPeriod, Previous: previous, Cut: shoptime.Clock(now)}, paid, nil
}

// dailySeries is the period's paid revenue and its paid orders, a bucket for
// each shop day.
func dailySeries(ctx context.Context, q *db.Queries, p period) (revenue, orders chart.Series, err error) {
	rows, err := q.PaidByShopDay(ctx, db.PaidByShopDayParams{
		FirstDay: shoptime.DayUTC(p.from), LastDay: shoptime.DayUTC(p.to.Add(-time.Nanosecond)),
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
