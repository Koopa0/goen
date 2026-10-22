// Package stockrisk is the list of SKUs ranked by how soon they run out, as
// three desks show it; it holds Row, NewRows, DaysCover, Rows and Section; it
// does not hold the estimate itself (inventory).
package stockrisk

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/product/inventory"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/chart"
)

// Row is one SKU's row as the back office shows it.
type Row struct {
	inventory.Risk

	// ReadAt is when the row was read: it decides whether a day is told with
	// its year.
	ReadAt time.Time
}

// NewRows wraps ranked rows read at readAt for display.
func NewRows(rows []inventory.Risk, readAt time.Time) []Row {
	out := make([]Row, 0, len(rows))
	for i := range rows {
		out = append(out, Row{Risk: rows[i], ReadAt: readAt})
	}
	return out
}

// DaysCover is a SKU's days cover as the back office shows it.
type DaysCover struct{ inventory.Cover }

// Estimate reads the SKU's days cover.
func (r Row) Estimate() DaysCover {
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

func (r Row) SellableText() string { return strconv.FormatInt(int64(r.Sellable), 10) }

// Facts is the line under the name: the SKU, what can be sold and what was. A
// sold out SKU has only its code: the badge and the day say the rest.
func (r Row) Facts(ctx context.Context, days int) string {
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
func (r Row) SoldOutSince(ctx context.Context) string {
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
func (r Row) SoldText(ctx context.Context, days int) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepSold), days,
		i18n.Count(ctx, i18n.KeyAdminRepUnitCount, r.Sold, r.Sold),
		i18n.Count(ctx, i18n.KeyAdminRepOrderCount, r.Orders, r.Orders))
}

func (r Row) Href() string { return "/admin/products/" + r.Slug }
