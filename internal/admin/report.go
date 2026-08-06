package admin

import (
	"context"
	"fmt"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
)

// ReportWindows are the periods the report offers.
//
// Three, and no date picker. A shop owner asks "how was this week" and "how was
// this month"; an arbitrary range is a different tool, and offering one here
// would make the page look like a BI product it is not.
var ReportWindows = []int32{7, 30, 90}

// DefaultWindow is what the page opens on.
const DefaultWindow int32 = 30

// MaxReportRows bounds each list.
const MaxReportRows = 10

// Report reads the numbers for one window.
//
// Four queries and not one: they answer different questions over different
// groupings, and folding them together would produce a single query nobody can
// read in order to save three round trips on a page a handful of people open.
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

// validWindow reports whether days is one the report offers.
//
// An allowlist, not a range: the window reaches a query that scans order
// history, so an arbitrary number from a URL is an arbitrary amount of work
// somebody can ask for.
func validWindow(days int32) bool {
	for _, w := range ReportWindows {
		if w == days {
			return true
		}
	}
	return false
}
