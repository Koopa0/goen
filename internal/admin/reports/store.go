package reports

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/chart"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

type Store struct {
	q *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("reports: NewStore requires a pool")
	}
	return &Store{q: db.New(pool)}
}

var reportWindows = [...]int32{7, 30, 90}

const DefaultWindow int32 = 30

const maxRows = 10

// ReportAt reads the last days shop days up to now, and the same number before
// them, cut at the same time of day.
func (s *Store) ReportAt(ctx context.Context, days int32, now time.Time) (admin.ReportView, error) {
	days = window(days)
	current, before := periods(now, int(days))

	revenue, err := s.q.RevenueBetween(ctx, db.RevenueBetweenParams{FromAt: current.from, ToAt: current.to})
	if err != nil {
		return admin.ReportView{}, fmt.Errorf("read revenue: %w", err)
	}
	completion, err := s.q.CheckoutCompletionBetween(ctx, db.CheckoutCompletionBetweenParams{
		FromAt: current.from, ToAt: current.to,
	})
	if err != nil {
		return admin.ReportView{}, fmt.Errorf("read completion: %w", err)
	}
	prevRevenue, err := s.q.RevenueBetween(ctx, db.RevenueBetweenParams{FromAt: before.from, ToAt: before.to})
	if err != nil {
		return admin.ReportView{}, fmt.Errorf("read previous revenue: %w", err)
	}
	prevCompletion, err := s.q.CheckoutCompletionBetween(ctx, db.CheckoutCompletionBetweenParams{
		FromAt: before.from, ToAt: before.to,
	})
	if err != nil {
		return admin.ReportView{}, fmt.Errorf("read previous completion: %w", err)
	}
	sellers, err := s.q.BestSellersBetween(ctx, db.BestSellersBetweenParams{
		FromAt: current.from, ToAt: current.to, LimitTo: maxRows,
	})
	if err != nil {
		return admin.ReportView{}, fmt.Errorf("read best sellers: %w", err)
	}
	risk, err := s.q.StockAtRisk(ctx, db.StockAtRiskParams{
		WindowDays: days, LimitTo: maxRows,
	})
	if err != nil {
		return admin.ReportView{}, fmt.Errorf("read stock at risk: %w", err)
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
		From:      shoptime.DateOf(current.from, now), To: shoptime.DateOf(current.to, now),
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
	for i := range risk {
		r := &risk[i]
		view.AtRisk = append(view.AtRisk, admin.StockRisk{
			SKU: r.SKU, Name: r.ProductName, Slug: r.Slug,
			Sellable: r.SellableQuantity,
			Sold:     r.UnitsSold, DaysCover: int(r.DaysCover),
		})
	}
	return view, nil
}

// DailyRevenue reads each shop day's paid revenue over the same two periods
// ReportAt reads, so the days add up to its revenue.
func (s *Store) DailyRevenue(ctx context.Context, days int32, now time.Time) (admin.DailyRevenue, error) {
	current, before := periods(now, int(window(days)))
	thisPeriod, err := s.dailySeries(ctx, current)
	if err != nil {
		return admin.DailyRevenue{}, err
	}
	previous, err := s.dailySeries(ctx, before)
	if err != nil {
		return admin.DailyRevenue{}, err
	}
	return admin.DailyRevenue{Current: thisPeriod, Previous: previous, Cut: shoptime.Clock(now)}, nil
}

func (s *Store) dailySeries(ctx context.Context, p period) (chart.Series, error) {
	rows, err := s.q.PaidByShopDay(ctx, db.PaidByShopDayParams{
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
