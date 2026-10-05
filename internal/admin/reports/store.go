package reports

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
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

func (s *Store) Report(ctx context.Context, days int32) (admin.ReportView, error) {
	if !validWindow(days) {
		days = DefaultWindow
	}

	revenue, err := s.q.RevenueSince(ctx, days)
	if err != nil {
		return admin.ReportView{}, fmt.Errorf("read revenue: %w", err)
	}
	completion, err := s.q.CheckoutCompletionSince(ctx, days)
	if err != nil {
		return admin.ReportView{}, fmt.Errorf("read completion: %w", err)
	}
	sellers, err := s.q.BestSellersSince(ctx, db.BestSellersSinceParams{
		WindowDays: days, LimitTo: maxRows,
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
		AverageCents: revenue.AverageCents,
		Placed:       completion.Placed,
		Committed:    completion.Committed,
		Windows:      windows,
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

// validWindow is an allowlist, never a range: it reaches a scanning query.
func validWindow(days int32) bool {
	return slices.Contains(reportWindows[:], days)
}
