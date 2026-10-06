package admin

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/chart"
)

const (
	// CoverWindowDays is the fewest shop days of sales a rate is read from: a
	// week is too short to tell a slow SKU from an idle one.
	CoverWindowDays = 30

	// coverMinOrders is the fewest orders that bought a SKU before its days
	// cover is estimated. Orders, not units: one order of ten units is one
	// event, and the interval is a count of events.
	coverMinOrders = 10

	// coverMinStocked is the fewest days in stock a rate is read from, so a
	// SKU just restocked is not estimated from a few days.
	coverMinStocked = 14 * 24 * time.Hour

	// coverWarnDays is the days cover under which a SKU is marked: the
	// restocking lead, not the period the report shows.
	coverWarnDays = 30

	// coverMaxDays is where the scale and every estimate end.
	coverMaxDays = 90

	// z90 is the normal quantile bounding a 90% interval on each side.
	z90 = 1.6448536269514722
)

// StockRisk is one SKU's stock and what was sold from it over the window.
type StockRisk struct {
	SKU      string
	Name     string
	Slug     string
	Sellable int32
	// Sold is units; Orders is how many orders bought it.
	Sold   int64
	Orders int64
	// InStock is how long over the window the SKU had anything to sell.
	InStock time.Duration
}

// CoverState says what can be said of a SKU's days cover.
type CoverState int

const (
	CoverSoldOut CoverState = iota
	CoverEstimated
	CoverFewOrders
	CoverJustStocked
)

// DaysCover is how many days a SKU's sellable stock lasts at its rate of sale,
// with the 90% range that rate allows. Days, Low and High are whole days and
// are set only when State is CoverEstimated.
type DaysCover struct {
	State           CoverState
	Days, Low, High int
}

// Estimate reads the SKU's days cover. A SKU with nothing sellable is sold out
// whatever it sold; the rate is units over the days it was in stock, and the
// range is the Poisson interval of the orders that made it.
func (r StockRisk) Estimate() DaysCover {
	switch {
	case r.Sellable <= 0:
		return DaysCover{State: CoverSoldOut}
	case r.Orders < coverMinOrders:
		return DaysCover{State: CoverFewOrders}
	case r.InStock < coverMinStocked:
		return DaysCover{State: CoverJustStocked}
	}
	perDay := float64(r.Sold) / (r.InStock.Hours() / 24)
	days := float64(r.Sellable) / perDay
	k := float64(r.Orders)
	slowest := chiSquareQuantile(-z90, 2*k) / (2 * k)
	fastest := chiSquareQuantile(z90, 2*k+2) / (2 * k)
	return DaysCover{
		State: CoverEstimated,
		Days:  int(math.Round(days)),
		Low:   int(math.Round(days / fastest)),
		High:  int(math.Round(days / slowest)),
	}
}

// chiSquareQuantile is the Wilson–Hilferty approximation of the chi-square
// quantile with dof degrees of freedom at normal quantile z.
func chiSquareQuantile(z, dof float64) float64 {
	a := 2 / (9 * dof)
	c := 1 - a + z*math.Sqrt(a)
	return dof * c * c * c
}

// Urgent reports whether the row is marked ▲.
func (c DaysCover) Urgent() bool {
	return c.State == CoverSoldOut || c.State == CoverEstimated && c.Days < coverWarnDays
}

// Beyond reports whether the estimate is past the end of the scale.
func (c DaysCover) Beyond() bool { return c.Days > coverMaxDays }

// Bar is the estimate and its range drawn on the one scale of every row, with
// the warning line at the days that mark ▲.
func (c DaysCover) Bar() chart.RangeBarProps {
	return chart.RangeBarProps{Value: int64(c.Days), Low: int64(c.Low), High: int64(c.High), Mark: coverWarnDays, Max: coverMaxDays}
}

// Figure is the estimate as it heads the row.
func (c DaysCover) Figure(ctx context.Context) string {
	switch c.State {
	case CoverSoldOut:
		return i18n.T(ctx, i18n.KeySoldOut)
	case CoverFewOrders:
		return i18n.T(ctx, i18n.KeyAdminRepFewSold)
	case CoverJustStocked:
		return i18n.T(ctx, i18n.KeyAdminRepNewStock)
	case CoverEstimated:
		if c.Beyond() {
			return i18n.Count(ctx, i18n.KeyAdminRepBeyond, coverMaxDays, coverMaxDays)
		}
		return i18n.Count(ctx, i18n.KeyAdminRepAbout, int64(c.Days), c.Days)
	}
	return ""
}

// Range is the 90% range in words, empty unless there is one to say.
func (c DaysCover) Range(ctx context.Context) string {
	if c.State != CoverEstimated || c.Beyond() {
		return ""
	}
	high := strconv.Itoa(c.High)
	if c.High > coverMaxDays {
		high = strconv.Itoa(coverMaxDays) + "+"
	}
	return i18n.Count(ctx, i18n.KeyAdminRepRange, int64(c.High), c.High, strconv.Itoa(c.Low), high)
}

// Warning is the sentence the ▲ stands for. It follows the range: the words are
// stronger when even the slowest end of it runs out within the line.
func (c DaysCover) Warning(ctx context.Context) string {
	if c.State != CoverEstimated || !c.Urgent() {
		return ""
	}
	if c.High < coverWarnDays {
		return i18n.Count(ctx, i18n.KeyAdminRepWithin, coverWarnDays, coverWarnDays)
	}
	return i18n.Count(ctx, i18n.KeyAdminRepMayRun, coverWarnDays, coverWarnDays)
}

func (r StockRisk) SellableText() string { return strconv.FormatInt(int64(r.Sellable), 10) }

// SoldText says what was sold over the window, in units and in orders.
func (r StockRisk) SoldText(ctx context.Context, days int) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepSold), days,
		i18n.Count(ctx, i18n.KeyAdminRepUnitCount, r.Sold, r.Sold),
		i18n.Count(ctx, i18n.KeyAdminRepOrderCount, r.Orders, r.Orders))
}

func (r StockRisk) Href() string { return "/admin/products/" + r.Slug }

// RankStockRisk puts sold out SKUs first, then the estimated ones from the
// shortest days cover, then those that cannot be estimated, and keeps the
// first limit.
func RankStockRisk(rows []StockRisk, limit int) []StockRisk {
	rank := func(r StockRisk) (group, days int) {
		c := r.Estimate()
		switch c.State {
		case CoverSoldOut:
			return 0, 0
		case CoverEstimated:
			return 1, c.Days
		case CoverFewOrders, CoverJustStocked:
			return 2, 0
		}
		return 2, 0
	}
	slices.SortStableFunc(rows, func(a, b StockRisk) int {
		ga, da := rank(a)
		gb, db := rank(b)
		return cmp.Or(
			cmp.Compare(ga, gb),
			cmp.Compare(da, db),
			cmp.Compare(a.Sellable, b.Sellable),
			cmp.Compare(b.Sold, a.Sold),
			cmp.Compare(a.SKU, b.SKU),
		)
	})
	return rows[:min(len(rows), limit)]
}
