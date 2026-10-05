package admin

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
)

type Seller struct {
	Slug         string
	Name         string
	Brand        string
	Units        int64
	RevenueCents int64
}

// Revenue formats product gross before order-level adjustments; the list labels this separately from total revenue.
func (s Seller) Revenue() string { return money.TWD(s.RevenueCents) }

func (s Seller) UnitsText() string { return strconv.FormatInt(s.Units, 10) }

func (s Seller) Href() string { return "/admin/products/" + s.Slug }

type StockRisk struct {
	SKU       string
	Name      string
	Slug      string
	Stock     int32
	Safety    int32
	Sold      int64
	DaysCover int
}

func (r StockRisk) Cover(ctx context.Context) string {
	return i18n.Count(ctx, i18n.KeyAdminDays, int64(r.DaysCover), r.DaysCover)
}

func (r StockRisk) Urgent() bool { return r.DaysCover <= 14 }

func (r StockRisk) StockText() string { return strconv.FormatInt(int64(r.Stock), 10) }

func (r StockRisk) SoldText() string { return strconv.FormatInt(r.Sold, 10) }

func (r StockRisk) Href() string { return "/admin/products/" + r.Slug }

type ReportView struct {
	Days         int
	Orders       int64
	RevenueCents int64
	// RefundedCents is what went back over the window, beside the revenue rather
	// than subtracted from it: Consumer Protection Act §19 makes a seven-day
	// rescission unrefusable, so an owner needs the return rate as much as the
	// net figure.
	RefundedCents int64
	AverageCents  int64
	Placed        int64
	Committed     int64
	Sellers       []Seller
	AtRisk        []StockRisk
	Windows       []int32
}

func (v ReportView) Revenue() string { return money.TWD(v.RevenueCents) }

func (v ReportView) Refunded() string { return money.TWD(v.RefundedCents) }

func (v ReportView) Average() string { return money.TWD(v.AverageCents) }

func (v ReportView) OrdersText() string { return strconv.FormatInt(v.Orders, 10) }

// minOrdersForRate is the fewest placed orders for which a percentage is
// shown; below it, one order moves the rate by more than five points.
const minOrdersForRate = 20

// Completion is the completion rate rounded half up, or the bare count when
// too few orders were placed for a percentage to mean anything. The rounded
// rate never reads 100% or 0% unless exactly all or none were committed.
func (v ReportView) Completion(ctx context.Context) string {
	switch {
	case v.Placed == 0:
		return "—"
	case v.ShowsCount():
		return v.Counts(ctx)
	}
	rate := (v.Committed*200 + v.Placed) / (v.Placed * 2)
	if v.Committed > 0 && v.Committed < v.Placed {
		rate = min(max(rate, 1), 99)
	}
	return strconv.FormatInt(rate, 10) + "%"
}

// Counts says how many of the placed orders were committed.
func (v ReportView) Counts(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepCounts), v.CommittedText(), v.PlacedText())
}

// ShowsCount reports whether Completion already states the counts.
func (v ReportView) ShowsCount() bool { return v.Placed < minOrdersForRate }

func (v ReportView) PlacedText() string { return strconv.FormatInt(v.Placed, 10) }

func (v ReportView) CommittedText() string { return strconv.FormatInt(v.Committed, 10) }

// Empty keeps refunds for older orders visible in a window with no new orders.
// Stock-at-risk is queried independently and remains visible in an empty window.
func (v ReportView) Empty() bool { return v.Placed == 0 && v.RefundedCents == 0 }

func (v ReportView) WindowHref(days int32) string {
	return "/admin/reports?days=" + strconv.FormatInt(int64(days), 10)
}

func (v ReportView) WindowLabel(ctx context.Context, days int32) string {
	return i18n.Count(ctx, i18n.KeyAdminDays, int64(days), days)
}

func (v ReportView) IsWindow(days int32) bool { return int(days) == v.Days }

// TopUnits is the longest bar's scale: the best seller's units.
func (v ReportView) TopUnits() int64 {
	var top int64
	for _, s := range v.Sellers {
		top = max(top, s.Units)
	}
	return top
}
