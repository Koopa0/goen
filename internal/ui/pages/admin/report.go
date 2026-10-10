package admin

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/chart"
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

// Facts is the line under the name: the brand, what was sold and for how much.
func (s Seller) Facts(ctx context.Context) string {
	var facts []string
	if s.Brand != "" {
		facts = append(facts, s.Brand)
	}
	return strings.Join(append(facts,
		i18n.Count(ctx, i18n.KeyAdminRepUnits, s.Units, s.UnitsText()),
		fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepGross), s.Revenue())), " · ")
}

func (s Seller) UnitsText() string { return strconv.FormatInt(s.Units, 10) }

func (s Seller) Href() string { return "/admin/products/" + s.Slug }

// Department is a top-level category and what its products' lines sold for.
type Department struct {
	Name       string
	SalesCents int64
}

func (d Department) Sales() string { return money.TWD(d.SalesCents) }

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
	Departments    []Department
	AtRisk         []StockRisk
	// StockDays is how many shop days back the stock rows look.
	StockDays int
	// MoreSoldOut counts the sold out SKUs the list leaves off.
	MoreSoldOut int
	Windows     []int32
	From, To    shoptime.Date
	Previous    PreviousFigures
	Returned    []ReturnedProduct
	// ReturnedErr is why Returned could not be read; the rest of the report
	// does not depend on it.
	ReturnedErr error
	Daily       DailyRevenue
	Paid        PaidDays
	// DailyUnavailable is set when the days could not be read, so the running
	// totals say so rather than read as a period without orders.
	DailyUnavailable bool
}

// DailyRevenue is each shop day's paid revenue in this period and in the one
// before it, and the time of day both periods are counted up to.
type DailyRevenue struct {
	Current, Previous chart.Series
	Cut               string
}

// PaidDays is the paid orders of each shop day of the period, the campaigns
// that were on during it, and, when no day had an order, the day of the latest
// one before it.
type PaidDays struct {
	Days      chart.Series
	Campaigns []chart.Span
	Latest    *shoptime.Date
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

func (v *ReportView) against(ctx context.Context, cur, prev int64, noise float64, prevText string) string {
	return against(ctx, v.Days, comparison{
		Orders: v.Orders, PreviousOrders: v.Previous.Orders, Noise: noise,
		Current: cur, Previous: prev, PreviousText: prevText,
	})
}

// comparison is a figure over a period set against the same figure over the
// period of as many days before it.
type comparison struct {
	Orders, PreviousOrders int64
	Current, Previous      int64
	// Noise is the variance of Current - Previous.
	Noise float64
	// PreviousText is the previous figure as it is written when no percentage is.
	PreviousText string
}

// against says by what percentage the figure differs from the previous period's
// only when both periods have orders enough for a percentage and the difference
// is at least two standard errors; otherwise it states the previous figure.
func against(ctx context.Context, days int, c comparison) string {
	if c.PreviousOrders == 0 {
		return noPreviousSentence(ctx, days)
	}
	diff := c.Current - c.Previous
	if c.Previous == 0 || c.Orders < minOrdersForRate || c.PreviousOrders < minOrdersForRate ||
		math.Abs(float64(diff)) < 2*math.Sqrt(c.Noise) {
		return previousSentence(ctx, days, c.PreviousText)
	}
	percent := (abs(diff)*200 + c.Previous) / (c.Previous * 2)
	switch {
	case percent == 0:
		return i18n.Count(ctx, i18n.KeyAdminRepSame, int64(days), days)
	case diff > 0:
		return i18n.Count(ctx, i18n.KeyAdminRepMore, int64(days), days, percent)
	}
	return i18n.Count(ctx, i18n.KeyAdminRepLess, int64(days), days, percent)
}

// ShowsRunningTotal reports whether this period has days with orders enough to
// draw its running total.
func (v *ReportView) ShowsRunningTotal() bool {
	return v.Daily.Current.Density() == chart.DensityFull
}

// RunningTotal is the chart of the days, with the revenue sentence as its
// caption: the tile's own wording, so the two cannot disagree.
func (v *ReportView) RunningTotal(ctx context.Context) chart.RunningTotalProps {
	current, previous := v.Daily.Current, v.Daily.Previous
	current.Label = i18n.Count(ctx, i18n.KeyAdminRepLastDays, int64(v.Days), v.Days)
	previous.Label = i18n.Count(ctx, i18n.KeyAdminRepPreviousDays, int64(v.Days), v.Days)
	return chart.RunningTotalProps{
		Current: current, Previous: previous,
		Measure: chart.MeasureMoney,
		Caption: i18n.Count(ctx, i18n.KeyAdminRepRunningCaption, int64(v.Days),
			v.Days, v.Revenue(), v.RevenueAgainst(ctx)),
		Note:               i18n.Count(ctx, i18n.KeyAdminRepRunningNote, int64(v.Days), v.Days, v.Daily.Cut),
		PreviousDayHeading: i18n.Count(ctx, i18n.KeyAdminRepPreviousDate, int64(v.Days), v.Days),
		DayHeading:         i18n.T(ctx, i18n.KeyAdminRepDate),
		TotalLabel:         i18n.T(ctx, i18n.KeyAdminRepTotal),
		PartialLabel:       fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepUntil), v.Daily.Cut),
	}
}

// PaidHeading names the chart of paid orders by what each of its columns holds.
func (v *ReportView) PaidHeading(ctx context.Context) string {
	if v.Paid.Days.Grouped() {
		return i18n.T(ctx, i18n.KeyAdminRepEvery7Days)
	}
	return i18n.T(ctx, i18n.KeyAdminRepDaily)
}

// ShowsPaidColumns reports whether the days with paid orders are enough to
// draw columns; fewer are told in a sentence.
func (v *ReportView) ShowsPaidColumns() bool {
	return v.Paid.Days.Density() >= chart.DensitySparse
}

// PaidColumns is the chart of paid orders, with the campaigns of the period
// bracketed on it.
func (v *ReportView) PaidColumns(ctx context.Context) chart.ColumnsProps {
	days := v.Paid.Days
	days.Label = i18n.T(ctx, i18n.KeyAdminRepPaidOrders)
	dayHeading := i18n.T(ctx, i18n.KeyAdminRepDate)
	if days.Grouped() {
		dayHeading = i18n.T(ctx, i18n.KeyAdminRepFromDay)
	}
	return chart.ColumnsProps{
		Series: days, Spans: v.Paid.Campaigns,
		Caption:      v.PaidSentence(ctx),
		Note:         fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepPaidNote), v.Daily.Cut),
		DayHeading:   dayHeading,
		SpanHeading:  i18n.T(ctx, i18n.KeyAdminRepCampaign),
		PartialLabel: fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepUntil), v.Daily.Cut),
	}
}

// PaidSentence says what the paid orders of the days were: the caption of the
// columns, or, when there are too few days to draw them, all there is to say.
func (v *ReportView) PaidSentence(ctx context.Context) string {
	days := v.Paid.Days
	period := dayText(ctx, v.From) + "–" + dayText(ctx, v.To)
	var withOrders []chart.Bucket
	for _, b := range days.Buckets {
		if b.Value != 0 {
			withOrders = append(withOrders, b)
		}
	}
	switch days.Density() {
	case chart.DensityNone:
		if v.Paid.Latest == nil {
			return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepNoPaid), period)
		}
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepNoPaidSince), period, longDayText(ctx, *v.Paid.Latest))
	case chart.DensityFew:
		items := make([]string, len(withOrders))
		for i, b := range withOrders {
			items[i] = i18n.Count(ctx, i18n.KeyAdminRepFewDay, b.Value, dayText(ctx, dayOf(b.Day)), b.Value)
		}
		return i18n.Count(ctx, i18n.KeyAdminRepFewDays, int64(len(withOrders)),
			period, len(withOrders), strings.Join(items, i18n.T(ctx, i18n.KeyChartListSeparator)))
	case chart.DensitySparse:
		return i18n.Count(ctx, i18n.KeyAdminRepSparseDays, int64(len(withOrders)), len(withOrders))
	case chart.DensityFull:
	}
	return v.busiest(ctx)
}

// busiest names the columns with the most paid orders; two or three tied are
// named, more are counted. Today is told beside the busiest day.
func (v *ReportView) busiest(ctx context.Context) string {
	days := v.Paid.Days
	peaks := chart.Peaks(days.Columns())
	orders := func(n int64) string { return i18n.Count(ctx, i18n.KeyAdminRepOrdersCount, n, n) }
	each := orders(peaks[0].Value)
	if days.Grouped() {
		if len(peaks) == 1 {
			return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepBusiestStretch), peaks[0].Days, dayText(ctx, dayOf(peaks[0].Day)), each)
		}
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepBusiestStretches), len(peaks), each)
	}
	today := orders(days.Buckets[len(days.Buckets)-1].Value)
	switch {
	case len(peaks) == 1:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepBusiestDay), longDayText(ctx, dayOf(peaks[0].Day)), each, today, v.Daily.Cut)
	case len(peaks) <= 3:
		names := make([]string, len(peaks))
		for i, c := range peaks {
			names[i] = longDayText(ctx, dayOf(c.Day))
		}
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepBusiestDays), strings.Join(names, i18n.T(ctx, i18n.KeyChartListSeparator)), each, today, v.Daily.Cut)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepBusiestMany), len(peaks), each, today, v.Daily.Cut)
}

func dayOf(t time.Time) shoptime.Date {
	return shoptime.Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}
}

// longDayText is a day as a sentence has it: "Oct 5", "10 月 5 日".
func longDayText(ctx context.Context, d shoptime.Date) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyShortDate), d.Month.String()[:3], int(d.Month), d.Day)
}

func (v *ReportView) previous(ctx context.Context, figure string) string {
	return previousSentence(ctx, v.Days, figure)
}

func (v *ReportView) noPrevious(ctx context.Context) string { return noPreviousSentence(ctx, v.Days) }

func previousSentence(ctx context.Context, days int, figure string) string {
	return i18n.Count(ctx, i18n.KeyAdminRepPrevious, int64(days), days, figure)
}

func noPreviousSentence(ctx context.Context, days int) string {
	return i18n.Count(ctx, i18n.KeyAdminRepNoPrevious, int64(days), days)
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

// TopDepartment is the longest bar's scale: the largest department's product sales.
func (v *ReportView) TopDepartment() int64 {
	var top int64
	for _, d := range v.Departments {
		top = max(top, d.SalesCents)
	}
	return top
}

// OnlyDepartment says so when one department holds all of the product sales, where a
// bar would compare it with nothing.
func (v *ReportView) OnlyDepartment(ctx context.Context) string {
	d := v.Departments[0]
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepDepartmentOnly), d.Name, d.Sales())
}
