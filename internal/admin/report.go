package admin

import (
	"context"
	"fmt"
	"slices"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
)

// ReportWindows are the periods the report offers.
var ReportWindows = []int32{7, 30, 90}

// DefaultWindow is what the page opens on.
const DefaultWindow int32 = 30

// MaxReportRows bounds each list.
const MaxReportRows = 10

// Report reads the numbers for one window.
func (s *Store) Report(ctx context.Context, days int32) (pages.AdminReportView, error) {
	if !validWindow(days) {
		days = DefaultWindow
	}

	revenue, err := s.q.RevenueSince(ctx, days)
	if err != nil {
		return pages.AdminReportView{}, fmt.Errorf("read revenue: %w", err)
	}
	completion, err := s.q.CheckoutCompletionSince(ctx, days)
	if err != nil {
		return pages.AdminReportView{}, fmt.Errorf("read completion: %w", err)
	}
	sellers, err := s.q.BestSellersSince(ctx, db.BestSellersSinceParams{
		WindowDays: days, LimitTo: MaxReportRows,
	})
	if err != nil {
		return pages.AdminReportView{}, fmt.Errorf("read best sellers: %w", err)
	}
	risk, err := s.q.StockAtRisk(ctx, db.StockAtRiskParams{
		WindowDays: days, LimitTo: MaxReportRows,
	})
	if err != nil {
		return pages.AdminReportView{}, fmt.Errorf("read stock at risk: %w", err)
	}

	view := pages.AdminReportView{
		Days:         int(days),
		Orders:       revenue.Orders,
		RevenueCents: revenue.RevenueCents,
		AverageCents: revenue.AverageCents,
		Placed:       completion.Placed,
		Committed:    completion.Committed,
		Windows:      ReportWindows,
	}
	for i := range sellers {
		r := &sellers[i]
		view.Sellers = append(view.Sellers, pages.AdminSeller{
			Slug: r.Slug, Name: r.Name, Brand: r.Brand,
			Units: r.Units, RevenueCents: r.RevenueCents,
		})
	}
	for i := range risk {
		r := &risk[i]
		view.AtRisk = append(view.AtRisk, pages.AdminStockRisk{
			SKU: r.SKU, Name: r.ProductName, Slug: r.Slug,
			Stock: r.StockQuantity, Safety: r.SafetyStock,
			Sold: r.UnitsSold, DaysCover: int(r.DaysCover),
		})
	}
	return view, nil
}

// validWindow is an allowlist, never a range: it reaches a scanning query.
func validWindow(days int32) bool {
	return slices.Contains(ReportWindows, days)
}
