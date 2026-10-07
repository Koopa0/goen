package admin

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
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

	// coverMaxRows is how many rows each of the two groups lists, sold out and
	// the rest, so neither can push the other off the page; the sold out ones
	// left off are counted.
	coverMaxRows = 10

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
	// SoldOutAt is when the SKU last ran out within the window; zero when it
	// was already out at its start.
	SoldOutAt time.Time
	// ReadAt is when the row was read: it decides whether a day is told with
	// its year.
	ReadAt time.Time
}

// CoverState says what can be said of a SKU's days cover.
type CoverState int

const (
	CoverSoldOut CoverState = iota
	CoverEstimated
	CoverFewOrders
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
	}
	// A SKU received minutes ago has few hours in stock; the floor keeps the
	// rate finite, and the estimate of such a SKU is one day.
	perDay := float64(r.Sold) / (max(r.InStock, time.Hour).Hours() / 24)
	days := float64(r.Sellable) / perDay
	k := float64(r.Orders)
	slowest := chiSquareQuantile(-z90, 2*k) / (2 * k)
	fastest := chiSquareQuantile(z90, 2*k+2) / (2 * k)
	// The interval is the orders' Poisson one, applied to a rate in units: it
	// takes the units per order as fixed, so with orders of varying size the
	// true spread is wider.
	return DaysCover{
		State: CoverEstimated,
		Days:  wholeDays(days),
		Low:   wholeDays(days / fastest),
		High:  wholeDays(days / slowest),
	}
}

// wholeDays rounds to the nearest day, and never to none: stock that lasts
// hours is a day.
func wholeDays(d float64) int { return max(int(math.Round(d)), 1) }

// chiSquareQuantile is the Wilson–Hilferty approximation of the chi-square
// quantile with dof degrees of freedom at normal quantile z.
func chiSquareQuantile(z, dof float64) float64 {
	a := 2 / (9 * dof)
	c := 1 - a + z*math.Sqrt(a)
	return dof * c * c * c
}

// Urgent reports whether the row is one to act on: sold out, or an estimate
// under the warning line. A sold out row shows its badge and an estimated one
// the ▲.
func (c DaysCover) Urgent() bool {
	return c.State == CoverSoldOut || c.State == CoverEstimated && c.Days < coverWarnDays
}

// Beyond reports whether the estimate is past the end of the scale.
func (c DaysCover) Beyond() bool { return c.Days > coverMaxDays }

// Bar is the estimate and how far it may reach, drawn on the one scale of every
// row, with a line at the days under which an estimate is marked ▲.
func (c DaysCover) Bar() chart.RangeBarProps {
	return chart.RangeBarProps{Value: int64(c.Days), High: int64(c.High), Mark: coverWarnDays, Max: coverMaxDays, Urgent: c.Urgent()}
}

// Figure is the estimate as it heads the row.
func (c DaysCover) Figure(ctx context.Context) string {
	switch c.State {
	case CoverSoldOut:
		return i18n.T(ctx, i18n.KeySoldOut)
	case CoverFewOrders:
		return i18n.T(ctx, i18n.KeyAdminRepFewSold)
	case CoverEstimated:
		if c.Beyond() {
			return i18n.Count(ctx, i18n.KeyAdminRepBeyond, coverMaxDays, coverMaxDays)
		}
		return i18n.Count(ctx, i18n.KeyAdminRepAbout, int64(c.Days), c.Days)
	}
	return ""
}

// Range is the 90% range in words, empty when there is none to say or none is
// drawn: a range that does not reach into the scale, or that is one day.
func (c DaysCover) Range(ctx context.Context) string {
	if c.State != CoverEstimated || c.Low >= coverMaxDays || c.Low == c.High {
		return ""
	}
	high := strconv.Itoa(c.High)
	if c.High > coverMaxDays {
		high = strconv.Itoa(coverMaxDays) + "+"
	}
	return i18n.Count(ctx, i18n.KeyAdminRepRange, int64(c.High), c.High, strconv.Itoa(c.Low), high)
}

// Warning is the sentence the ▲ stands for, which is its text alternative. It
// follows the range: the words are stronger when even the slowest end of it runs
// out within the line.
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

// Facts is the line under the name: the SKU, what can be sold and what was. A
// sold out SKU has only its code: the badge and the day say the rest.
func (r StockRisk) Facts(ctx context.Context, days int) string {
	if r.Sellable <= 0 {
		return r.SKU
	}
	return strings.Join([]string{
		r.SKU,
		fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepLeft), r.SellableText()),
		r.SoldText(ctx, days),
	}, " · ")
}

// SoldOutSince is the day it ran out, empty when it was out before the window.
func (r StockRisk) SoldOutSince(ctx context.Context) string {
	if r.SoldOutAt.IsZero() {
		return ""
	}
	day := shoptime.DateOf(r.SoldOutAt, r.ReadAt)
	text := shoptime.DateLabel(ctx, day)
	if day.OtherYear {
		text = shoptime.DateText(ctx, day)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepSoldOutSince), text)
}

// SoldText says what was sold over the window, in units and in orders.
func (r StockRisk) SoldText(ctx context.Context, days int) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepSold), days,
		i18n.Count(ctx, i18n.KeyAdminRepUnitCount, r.Sold, r.Sold),
		i18n.Count(ctx, i18n.KeyAdminRepOrderCount, r.Orders, r.Orders))
}

func (r StockRisk) Href() string { return "/admin/products/" + r.Slug }

// RankStockRisk lists the sold out SKUs that ran out most recently, then the
// estimated ones from the shortest days cover, then those that cannot be
// estimated, each of the two groups at most coverMaxRows. It also returns how
// many sold out SKUs it left off.
func RankStockRisk(rows []StockRisk) (listed []StockRisk, moreSoldOut int) {
	var soldOut, rest []StockRisk
	for i := range rows {
		if rows[i].Estimate().State == CoverSoldOut {
			soldOut = append(soldOut, rows[i])
		} else {
			rest = append(rest, rows[i])
		}
	}
	slices.SortStableFunc(soldOut, func(a, b StockRisk) int {
		return cmp.Or(b.SoldOutAt.Compare(a.SoldOutAt), cmp.Compare(b.Sold, a.Sold), cmp.Compare(a.SKU, b.SKU))
	})
	slices.SortStableFunc(rest, func(a, b StockRisk) int {
		ea, eb := a.Estimate(), b.Estimate()
		return cmp.Or(
			cmp.Compare(ea.State, eb.State),
			cmp.Compare(ea.Days, eb.Days),
			cmp.Compare(a.Sellable, b.Sellable),
			cmp.Compare(b.Sold, a.Sold),
			cmp.Compare(a.SKU, b.SKU),
		)
	})
	shown := min(len(soldOut), coverMaxRows)
	listed = make([]StockRisk, 0, shown+min(len(rest), coverMaxRows))
	listed = append(listed, soldOut[:shown]...)
	listed = append(listed, rest[:min(len(rest), coverMaxRows)]...)
	return listed, len(soldOut) - shown
}

// dashboardRunwayRows is how many of the days cover rows the dashboard lists.
const dashboardRunwayRows = 5

// RunwayBasis says what an empty dashboard runway can truthfully say.
type RunwayBasis uint8

const (
	// RunwayTooFewSales: no SKU in stock could be estimated yet.
	RunwayTooFewSales RunwayBasis = iota
	// RunwayEstimated: some SKU was estimated.
	RunwayEstimated
	// RunwayNothingInStock: every SKU that sold in the window is sold out.
	RunwayNothingInStock
)

// DashboardRunway keeps the first rows of a ranked days cover list that run out
// within the warning line: the sold-out task row already counts the sold out
// ones and links to them, and an estimate past the line is not what the
// section's title promises. cut reports that rows were left off; basis tells an
// empty list apart: nothing is running out, nothing can be estimated yet, or
// nothing that sold in the window is left in stock.
func DashboardRunway(listed []StockRisk) (kept []StockRisk, cut bool, basis RunwayBasis) {
	inStock := false
	for i := range listed {
		cover := listed[i].Estimate()
		if cover.State != CoverSoldOut {
			inStock = true
		}
		if cover.State != CoverEstimated {
			continue
		}
		basis = RunwayEstimated
		if !cover.Urgent() {
			continue
		}
		if len(kept) == dashboardRunwayRows {
			return kept, true, basis
		}
		kept = append(kept, listed[i])
	}
	if basis == RunwayTooFewSales && len(listed) > 0 && !inStock {
		basis = RunwayNothingInStock
	}
	return kept, false, basis
}
