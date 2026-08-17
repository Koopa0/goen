package pages

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
)

// AdminSeller is one product's sales over the window.
type AdminSeller struct {
	Slug         string
	Name         string
	Brand        string
	Units        int64
	RevenueCents int64
}

// Revenue is what it brought in.
func (s AdminSeller) Revenue() string { return twd(s.RevenueCents) }

// UnitsText is how many went out.
func (s AdminSeller) UnitsText() string { return strconv.FormatInt(s.Units, 10) }

// Href is the product's page.
func (s AdminSeller) Href() string { return "/admin/products/" + s.Slug }

// AdminStockRisk is a variant selling faster than its stock will last.
type AdminStockRisk struct {
	SKU       string
	Name      string
	Slug      string
	Stock     int32
	Safety    int32
	Sold      int64
	DaysCover int
}

// Cover is the days of stock left, in words.
func (r AdminStockRisk) Cover(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminDays), r.DaysCover)
}

// Urgent reports whether it runs out inside the fortnight a reorder takes.
func (r AdminStockRisk) Urgent() bool { return r.DaysCover <= 14 }

// StockText is what is on the shelf.
func (r AdminStockRisk) StockText() string { return strconv.FormatInt(int64(r.Stock), 10) }

// SoldText is how many went out over the window.
func (r AdminStockRisk) SoldText() string { return strconv.FormatInt(r.Sold, 10) }

// Href is the product's page.
func (r AdminStockRisk) Href() string { return "/admin/products/" + r.Slug }

// AdminReportView is the numbers page.
type AdminReportView struct {
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
	Sellers       []AdminSeller
	AtRisk        []AdminStockRisk
	Windows       []int32
}

// Revenue is what came in over the window.
func (v AdminReportView) Revenue() string { return twd(v.RevenueCents) }

// Refunded is what went back over the window.
func (v AdminReportView) Refunded() string { return twd(v.RefundedCents) }

// Average is the average committed order.
func (v AdminReportView) Average() string { return twd(v.AverageCents) }

// OrdersText is how many committed orders there were.
func (v AdminReportView) OrdersText() string { return strconv.FormatInt(v.Orders, 10) }

// Completion is the percentage of started orders that were paid for.
func (v AdminReportView) Completion() string {
	if v.Placed == 0 {
		return "—"
	}
	return strconv.FormatInt(v.Committed*100/v.Placed, 10) + "%"
}

// PlacedText is how many orders were started.
func (v AdminReportView) PlacedText() string { return strconv.FormatInt(v.Placed, 10) }

// CommittedText is how many were paid for.
func (v AdminReportView) CommittedText() string { return strconv.FormatInt(v.Committed, 10) }

// Empty reports whether the window contains nothing at all.
func (v AdminReportView) Empty() bool { return v.Placed == 0 }

// WindowHref is the link to another window.
func (v AdminReportView) WindowHref(days int32) string {
	return "/admin/reports?days=" + strconv.FormatInt(int64(days), 10)
}

// WindowLabel is what that link says.
func (v AdminReportView) WindowLabel(ctx context.Context, days int32) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminDays), days)
}

// IsWindow reports whether days is the one being shown.
func (v AdminReportView) IsWindow(days int32) bool { return int(days) == v.Days }
