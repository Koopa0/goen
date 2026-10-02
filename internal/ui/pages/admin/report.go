package admin

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
)

// Seller is one product's sales over the window.
type Seller struct {
	Slug         string
	Name         string
	Brand        string
	Units        int64
	RevenueCents int64
}

// Revenue formats product gross before order-level adjustments; the list labels this separately from total revenue.
func (s Seller) Revenue() string { return money.TWD(s.RevenueCents) }

// UnitsText is how many went out.
func (s Seller) UnitsText() string { return strconv.FormatInt(s.Units, 10) }

// Href is the product's page.
func (s Seller) Href() string { return "/admin/products/" + s.Slug }

// StockRisk is a variant selling faster than its stock will last.
type StockRisk struct {
	SKU       string
	Name      string
	Slug      string
	Stock     int32
	Safety    int32
	Sold      int64
	DaysCover int
}

// Cover is the days of stock left, in words.
func (r StockRisk) Cover(ctx context.Context) string {
	return i18n.Count(ctx, i18n.KeyAdminDays, int64(r.DaysCover), r.DaysCover)
}

// Urgent reports whether it runs out inside the fortnight a reorder takes.
func (r StockRisk) Urgent() bool { return r.DaysCover <= 14 }

// StockText is what is on the shelf.
func (r StockRisk) StockText() string { return strconv.FormatInt(int64(r.Stock), 10) }

// SoldText is how many went out over the window.
func (r StockRisk) SoldText() string { return strconv.FormatInt(r.Sold, 10) }

// Href is the product's page.
func (r StockRisk) Href() string { return "/admin/products/" + r.Slug }

// ReportView is the numbers page.
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

// Revenue is what came in over the window.
func (v ReportView) Revenue() string { return money.TWD(v.RevenueCents) }

// Refunded is what went back over the window.
func (v ReportView) Refunded() string { return money.TWD(v.RefundedCents) }

// Average is the average committed order.
func (v ReportView) Average() string { return money.TWD(v.AverageCents) }

// OrdersText is how many committed orders there were.
func (v ReportView) OrdersText() string { return strconv.FormatInt(v.Orders, 10) }

// Completion is the percentage of started orders that were paid for.
func (v ReportView) Completion() string {
	if v.Placed == 0 {
		return "—"
	}
	return strconv.FormatInt(v.Committed*100/v.Placed, 10) + "%"
}

// PlacedText is how many orders were started.
func (v ReportView) PlacedText() string { return strconv.FormatInt(v.Placed, 10) }

// CommittedText is how many were paid for.
func (v ReportView) CommittedText() string { return strconv.FormatInt(v.Committed, 10) }

// Empty keeps refunds for older orders visible in a window with no new orders.
// Stock-at-risk is queried independently and remains visible in an empty window.
func (v ReportView) Empty() bool { return v.Placed == 0 && v.RefundedCents == 0 }

// WindowHref is the link to another window.
func (v ReportView) WindowHref(days int32) string {
	return "/admin/reports?days=" + strconv.FormatInt(int64(days), 10)
}

// WindowLabel is what that link says.
func (v ReportView) WindowLabel(ctx context.Context, days int32) string {
	return i18n.Count(ctx, i18n.KeyAdminDays, int64(days), days)
}

// IsWindow reports whether days is the one being shown.
func (v ReportView) IsWindow(days int32) bool { return int(days) == v.Days }
