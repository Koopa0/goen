package reports

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/shoptime"
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

// Report reads the last days shop days up to now, and the same number before
// them, cut at the same time of day.
func (s *Store) Report(ctx context.Context, days int32) (admin.ReportView, error) {
	return s.ReportAt(ctx, days, time.Now())
}

// ReportAt is Report as of now.
func (s *Store) ReportAt(ctx context.Context, days int32, now time.Time) (admin.ReportView, error) {
	if !validWindow(days) {
		days = DefaultWindow
	}
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
	atRisk, err := s.stockAtRisk(ctx, int(days), now)
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
		AtRisk:    atRisk, StockDays: max(int(days), admin.CoverWindowDays),
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

// validWindow is an allowlist, never a range: it reaches a scanning query.
func validWindow(days int32) bool {
	return slices.Contains(reportWindows[:], days)
}

// stockAtRisk ranks the SKUs that sold or are sold out over the last shop days,
// at least admin.CoverWindowDays of them whichever period the report shows.
func (s *Store) stockAtRisk(ctx context.Context, days int, now time.Time) ([]admin.StockRisk, error) {
	window, _ := periods(now, max(days, admin.CoverWindowDays))
	rows, err := s.q.StockAtRisk(ctx, db.StockAtRiskParams{FromAt: window.from, ToAt: window.to})
	if err != nil {
		return nil, fmt.Errorf("read stock at risk: %w", err)
	}
	var sellable []uuid.UUID
	for i := range rows {
		if rows[i].StockQuantity > rows[i].SafetyStock {
			sellable = append(sellable, rows[i].VariantID)
		}
	}
	moves := map[uuid.UUID][]movement{}
	if len(sellable) > 0 {
		ledger, err := s.q.StockMovementsSince(ctx, db.StockMovementsSinceParams{
			VariantIds: sellable, FromAt: window.from,
		})
		if err != nil {
			return nil, fmt.Errorf("read stock movements: %w", err)
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
			InStock: timeInStock(r.StockQuantity, r.SafetyStock,
				latest(window.from, r.ListedAt), window.to, moves[r.VariantID]),
		})
	}
	return admin.RankStockRisk(risk, maxRows), nil
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
