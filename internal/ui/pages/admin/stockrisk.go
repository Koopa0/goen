package admin

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/inventory"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/chart"
)

// StockRisk is one SKU's row as the back office shows it.
type StockRisk struct {
	inventory.Risk

	// ReadAt is when the row was read: it decides whether a day is told with
	// its year.
	ReadAt time.Time
}

// StockRisks wraps ranked rows read at readAt for display.
func StockRisks(rows []inventory.Risk, readAt time.Time) []StockRisk {
	out := make([]StockRisk, 0, len(rows))
	for i := range rows {
		out = append(out, StockRisk{Risk: rows[i], ReadAt: readAt})
	}
	return out
}

// DaysCover is a SKU's days cover as the back office shows it.
type DaysCover struct{ inventory.Cover }

// Estimate reads the SKU's days cover.
func (r StockRisk) Estimate() DaysCover {
	return DaysCover{r.Sales.Estimate()}
}

// Bar is the estimate and how far it may reach, drawn on the one scale of every
// row, with a line at the days under which an estimate is marked ▲.
func (c DaysCover) Bar() chart.RangeBarProps {
	return chart.RangeBarProps{Value: int64(c.Days), High: int64(c.High), Mark: inventory.CoverWarnDays, Max: inventory.CoverMaxDays, Urgent: c.Urgent()}
}

// Figure is the estimate as it heads the row.
func (c DaysCover) Figure(ctx context.Context) string {
	switch c.State {
	case inventory.CoverSoldOut:
		return i18n.T(ctx, i18n.KeySoldOut)
	case inventory.CoverFewOrders:
		return i18n.T(ctx, i18n.KeyAdminRepFewSold)
	case inventory.CoverEstimated:
		if c.Beyond() {
			return i18n.Count(ctx, i18n.KeyAdminRepBeyond, inventory.CoverMaxDays, inventory.CoverMaxDays)
		}
		return i18n.Count(ctx, i18n.KeyAdminRepAbout, int64(c.Days), c.Days)
	}
	return ""
}

// Range is the 90% range in words, empty when there is none to say or none is
// drawn: a range that does not reach into the scale, or that is one day.
func (c DaysCover) Range(ctx context.Context) string {
	if c.State != inventory.CoverEstimated || c.Low >= inventory.CoverMaxDays || c.Low == c.High {
		return ""
	}
	high := strconv.Itoa(c.High)
	if c.High > inventory.CoverMaxDays {
		high = strconv.Itoa(inventory.CoverMaxDays) + "+"
	}
	return i18n.Count(ctx, i18n.KeyAdminRepRange, int64(c.High), c.High, strconv.Itoa(c.Low), high)
}

// Warning is the sentence the ▲ stands for, which is its text alternative. It
// follows the range: the words are stronger when even the slowest end of it runs
// out within the line.
func (c DaysCover) Warning(ctx context.Context) string {
	if c.State != inventory.CoverEstimated || !c.Urgent() {
		return ""
	}
	if c.High < inventory.CoverWarnDays {
		return i18n.Count(ctx, i18n.KeyAdminRepWithin, inventory.CoverWarnDays, inventory.CoverWarnDays)
	}
	return i18n.Count(ctx, i18n.KeyAdminRepMayRun, inventory.CoverWarnDays, inventory.CoverWarnDays)
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
		if cover.State != inventory.CoverSoldOut {
			inStock = true
		}
		if cover.State != inventory.CoverEstimated {
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
