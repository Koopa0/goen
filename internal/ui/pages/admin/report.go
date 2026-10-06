package admin

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/shoptime"
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
	// RevenueSquares is the sum of the squared order totals, which the noise
	// of RevenueCents is read from.
	RevenueSquares float64
	Sellers        []Seller
	AtRisk         []StockRisk
	// StockDays is how many shop days back the stock rows look.
	StockDays int
	// MoreSoldOut counts the sold out SKUs the list leaves off.
	MoreSoldOut int
	Windows     []int32
	From, To    shoptime.Date
	Previous    PreviousFigures
}

// PreviousFigures are the period of as many shop days before this one, up to
// the same time of day.
type PreviousFigures struct {
	From, To       shoptime.Date
	Orders         int64
	RevenueCents   int64
	RevenueSquares float64
	RefundedCents  int64
	AverageCents   int64
	Placed         int64
	Committed      int64
}

// Heading names the period and the one it is set against, by shop day.
func (v *ReportView) Heading(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepPeriods),
		dayText(ctx, v.From), dayText(ctx, v.To), dayText(ctx, v.Previous.From), dayText(ctx, v.Previous.To))
}

func dayText(ctx context.Context, d shoptime.Date) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepDay), d.Month.String()[:3], int(d.Month), d.Day)
}

// RevenueAgainst is the line under the revenue figure.
func (v *ReportView) RevenueAgainst(ctx context.Context) string {
	return v.against(ctx, v.RevenueCents, v.Previous.RevenueCents,
		v.RevenueSquares+v.Previous.RevenueSquares, money.TWD(v.Previous.RevenueCents))
}

// OrdersAgainst is the line under the paid orders figure.
func (v *ReportView) OrdersAgainst(ctx context.Context) string {
	return v.against(ctx, v.Orders, v.Previous.Orders,
		float64(v.Orders+v.Previous.Orders), strconv.FormatInt(v.Previous.Orders, 10))
}

// AverageAgainst is the line under the average order figure: the previous
// figure only, since no test tells a real change in an average from noise.
func (v *ReportView) AverageAgainst(ctx context.Context) string {
	if v.Previous.Orders == 0 {
		return v.noPrevious(ctx)
	}
	return v.previous(ctx, money.TWD(v.Previous.AverageCents))
}

func (v *ReportView) RefundedAgainst(ctx context.Context) string {
	return v.previous(ctx, money.TWD(v.Previous.RefundedCents))
}

func (v *ReportView) CompletionAgainst(ctx context.Context) string {
	if v.Previous.Placed == 0 {
		return i18n.Count(ctx, i18n.KeyAdminRepNoOrders, int64(v.Days), v.Days)
	}
	before := ReportView{Placed: v.Previous.Placed, Committed: v.Previous.Committed}
	return v.previous(ctx, before.Completion(ctx))
}

// against says by what percentage cur differs from prev only when both periods
// have orders enough for a percentage and the difference is at least two
// standard errors, where noise is the variance of cur - prev; otherwise it
// states the previous figure.
func (v *ReportView) against(ctx context.Context, cur, prev int64, noise float64, prevText string) string {
	if v.Previous.Orders == 0 {
		return v.noPrevious(ctx)
	}
	diff := cur - prev
	if prev == 0 || v.Orders < minOrdersForRate || v.Previous.Orders < minOrdersForRate ||
		math.Abs(float64(diff)) < 2*math.Sqrt(noise) {
		return v.previous(ctx, prevText)
	}
	days := v.Days
	percent := (abs(diff)*200 + prev) / (prev * 2)
	switch {
	case percent == 0:
		return i18n.Count(ctx, i18n.KeyAdminRepSame, int64(days), days)
	case diff > 0:
		return i18n.Count(ctx, i18n.KeyAdminRepMore, int64(days), days, percent)
	}
	return i18n.Count(ctx, i18n.KeyAdminRepLess, int64(days), days, percent)
}

func (v *ReportView) previous(ctx context.Context, figure string) string {
	return i18n.Count(ctx, i18n.KeyAdminRepPrevious, int64(v.Days), v.Days, figure)
}

func (v *ReportView) noPrevious(ctx context.Context) string {
	return i18n.Count(ctx, i18n.KeyAdminRepNoPrevious, int64(v.Days), v.Days)
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func (v *ReportView) Revenue() string { return money.TWD(v.RevenueCents) }

func (v *ReportView) Refunded() string { return money.TWD(v.RefundedCents) }

func (v *ReportView) Average() string { return money.TWD(v.AverageCents) }

func (v *ReportView) OrdersText() string { return strconv.FormatInt(v.Orders, 10) }

// minOrdersForRate is the fewest orders a period needs before its figures are
// set against another's as a percentage: below it, one order moves the
// completion rate by more than five points and the paid-order and revenue
// comparisons are noise.
const minOrdersForRate = 20

// Completion is the completion rate rounded half up, or the bare count when
// too few orders were placed for a percentage to mean anything. The rounded
// rate never reads 100% or 0% unless exactly all or none were committed.
func (v *ReportView) Completion(ctx context.Context) string {
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
func (v *ReportView) Counts(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepCounts), v.CommittedText(), v.PlacedText())
}

// ShowsCount reports whether Completion already states the counts.
func (v *ReportView) ShowsCount() bool { return v.Placed < minOrdersForRate }

func (v *ReportView) PlacedText() string { return strconv.FormatInt(v.Placed, 10) }

func (v *ReportView) CommittedText() string { return strconv.FormatInt(v.Committed, 10) }

// Empty keeps refunds for older orders visible in a window with no new orders.
// Stock-at-risk is queried independently and remains visible in an empty window.
func (v *ReportView) Empty() bool { return v.Placed == 0 && v.RefundedCents == 0 }

func (v *ReportView) WindowHref(days int32) string {
	return "/admin/reports?days=" + strconv.FormatInt(int64(days), 10)
}

func (v *ReportView) WindowLabel(ctx context.Context, days int32) string {
	return i18n.Count(ctx, i18n.KeyAdminDays, int64(days), days)
}

func (v *ReportView) IsWindow(days int32) bool { return int(days) == v.Days }

// TopUnits is the longest bar's scale: the best seller's units.
func (v *ReportView) TopUnits() int64 {
	var top int64
	for _, s := range v.Sellers {
		top = max(top, s.Units)
	}
	return top
}
