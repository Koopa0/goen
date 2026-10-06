package chart

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
)

const (
	plotHeight = 136 // the value area, in pixels
	plotTop    = 8   // room above the highest line
	axisBand   = 26  // the labels under the baseline
	endGap     = 30  // the least distance between the two end labels
)

// RunningTotalProps is two series of daily values drawn as running totals from
// zero, this period over the one before it. Both are as long as the period.
// Caption and Note are the page's sentences, already localised; so are the
// column heads of the table and PartialLabel, which says what the last row is
// counted up to when Current.Partial.
type RunningTotalProps struct {
	Current, Previous  Series
	Measure            Measure
	Caption, Note      string
	DayHeading         string
	PreviousDayHeading string
	TotalLabel         string
	PartialLabel       string
}

type gridLine struct {
	Y        float64
	Label    string
	Baseline bool
}

type endLabel struct {
	Name, Total string
	NameY       float64
}

type endPoint struct {
	Y    float64
	Open bool
}

type dayTick struct {
	X      string
	Anchor string
	Label  string
	Minor  bool
	Today  bool
}

type tableRow struct {
	Heading, Current, PreviousDay, Previous string
}

// axes is the value axis and the day axis a chart stands on.
type axes struct {
	Height           int
	Baseline, LabelY float64
	Grid             []gridLine
	Ticks            []dayTick
}

// newAxes lays out the axes for values from 0 to top and the shop days of
// buckets, with top of the plot plotTop pixels down. It returns the axes and
// where on them a value sits.
func newAxes(ctx context.Context, top int64, m Measure, buckets []Bucket, plotTop float64) (a axes, y func(int64) float64) {
	step := axisStep(top, m)
	lines := gridLines(top, step)
	axisTop := step * lines

	whole := axisTop
	if m == MeasureMoney {
		whole /= 100
	}
	divisor, suffix := i18n.AxisUnit(ctx, whole)

	y = func(v int64) float64 {
		return plotTop + plotHeight*(1-float64(v)/float64(axisTop))
	}
	baseline := y(0)
	a = axes{
		Height: int(plotTop) + plotHeight + axisBand, Baseline: baseline, LabelY: baseline + axisBand - 8,
		Ticks: ticksFor(ctx, buckets),
	}
	for k := range lines + 1 {
		v := k * step
		a.Grid = append(a.Grid, gridLine{Y: y(v), Label: m.axisText(v, divisor, suffix), Baseline: k == 0})
	}
	return a, y
}

// runningTotal is everything RunningTotal draws, worked out.
type runningTotal struct {
	axes

	Area, CurrentLine, PrevLine string
	CurrentEnd, PreviousEnd     endPoint
	CurrentLabel, PrevLabel     endLabel
	Rows                        []tableRow
	Total                       tableRow
}

func (m Measure) text(v int64) string {
	if m == MeasureMoney {
		return money.TWD(v)
	}
	return strconv.FormatInt(v, 10)
}

// axisText is a value on the axis, counted in the axis' one unit. money.Short
// counts hundredths, so a count is scaled up to match.
func (m Measure) axisText(v, divisor int64, suffix string) string {
	if v == 0 {
		return "0"
	}
	if m == MeasureCount {
		v *= 100
	}
	return money.Short(v, divisor) + suffix
}

// cumulative is the running sum of the series' values after each day.
func cumulative(s Series) []int64 {
	sums := make([]int64, len(s.Buckets))
	var sum int64
	for i, b := range s.Buckets {
		sum += b.Value
		sums[i] = sum
	}
	return sums
}

func lastOf(sums []int64) int64 {
	if len(sums) == 0 {
		return 0
	}
	return sums[len(sums)-1]
}

func newRunningTotal(ctx context.Context, p *RunningTotalProps) runningTotal {
	current, previous := cumulative(p.Current), cumulative(p.Previous)
	n := len(current)

	ax, y := newAxes(ctx, max(lastOf(current), lastOf(previous)), p.Measure, p.Current.Buckets, plotTop)
	baseline := ax.Baseline
	r := runningTotal{axes: ax}

	line := func(sums []int64) string {
		var b strings.Builder
		fmt.Fprintf(&b, "0,%s", px(baseline))
		for i, v := range sums[:min(len(sums), n)] {
			fmt.Fprintf(&b, " %d,%s", (i+1)*1000/n, px(y(v)))
		}
		return b.String()
	}
	r.CurrentLine = line(current)
	r.PrevLine = line(previous)
	r.Area = r.CurrentLine + " 1000," + px(baseline)

	yc, yp := y(lastOf(current)), y(lastOf(previous))
	r.CurrentEnd = endPoint{Y: yc, Open: p.Current.Partial}
	r.PreviousEnd = endPoint{Y: yp, Open: p.Previous.Partial}
	nameC, nameP := apart(yc, yp, plotTop+12, baseline)
	r.CurrentLabel = endLabel{Name: p.Current.Label, Total: p.Measure.text(lastOf(current)), NameY: nameC}
	r.PrevLabel = endLabel{Name: p.Previous.Label, Total: p.Measure.text(lastOf(previous)), NameY: nameP}

	for i, b := range p.Current.Buckets {
		heading := axisDay(ctx, b.Day)
		if i == n-1 && p.Current.Partial && p.PartialLabel != "" {
			heading += " (" + p.PartialLabel + ")"
		}
		row := tableRow{Heading: heading, Current: p.Measure.text(current[i])}
		if i < len(previous) {
			row.PreviousDay = axisDay(ctx, p.Previous.Buckets[i].Day)
			row.Previous = p.Measure.text(previous[i])
		}
		r.Rows = append(r.Rows, row)
	}
	r.Total = tableRow{
		Heading: p.TotalLabel,
		Current: p.Measure.text(lastOf(current)), Previous: p.Measure.text(lastOf(previous)),
	}
	return r
}

// apart moves two labels' baselines at least endGap from each other, around the
// middle of where they would sit, and keeps both between low and high. It
// returns where a and b end up.
func apart(a, b, low, high float64) (movedA, movedB float64) {
	swapped := a > b
	if swapped {
		a, b = b, a
	}
	if b-a < endGap {
		mid := (a + b) / 2
		a, b = mid-endGap/2, mid+endGap/2
	}
	if shift := low - a; shift > 0 {
		a, b = a+shift, b+shift
	}
	if shift := b - high; shift > 0 {
		a, b = a-shift, b-shift
	}
	if swapped {
		return b, a
	}
	return a, b
}

func axisDay(ctx context.Context, day time.Time) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyChartAxisDay), day.Month().String()[:3], int(day.Month()), day.Day())
}

// ticksFor labels the axis: the first day, the days tickDays picks, and today
// at the end. Only the pick nearest the middle stays on a narrow chart.
func ticksFor(ctx context.Context, buckets []Bucket) []dayTick {
	n := len(buckets)
	days := make([]time.Time, n)
	for i, b := range buckets {
		days[i] = b.Day
	}
	picked := tickDays(days)
	nearest := -1
	for _, i := range picked {
		if nearest < 0 || abs(2*i-n) < abs(2*nearest-n) {
			nearest = i
		}
	}
	ticks := []dayTick{{X: "0%", Anchor: "start", Label: axisDay(ctx, days[0])}}
	for _, i := range picked {
		ticks = append(ticks, dayTick{
			X: percent(float64(i) * 100 / float64(n)), Anchor: "middle",
			Label: axisDay(ctx, days[i]), Minor: i != nearest,
		})
	}
	return append(ticks, dayTick{X: "100%", Anchor: "end", Label: i18n.T(ctx, i18n.KeyChartToday), Today: true})
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func px(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }

func percent(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) + "%" }
