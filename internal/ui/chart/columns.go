package chart

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/i18n"
)

const (
	maxDailyColumns = 45  // beyond it, a column holds daysPerColumn days
	daysPerColumn   = 7   // counted back from the last day
	columnsPlot     = 150 // the value area of the columns, in pixels
	laneHeight      = 26  // a campaign's lane: its name, then its bracket under it, in pixels
	laneTop         = 4   // the room above the first lane
	laneNameBase    = 12  // a lane's name baseline, from the top of the lane
	laneBracketAt   = 18  // a lane's bracket line, from the top of the lane
	minValueRoom    = 8   // the least of it, when the highest bar stops short of the top
	valueRoom       = 22  // between the last lane and the columns, for the value over the highest
	maxLanes        = 3   // a campaign that finds no free lane among them is shaded without a bracket or a name
	narrowPlot      = 244 // the narrowest plot that draws the strips' names, in pixels at 12px text, which they are fitted to
	desktopPlot     = 888 // the width of a plot at its widest, in pixels
	widestMinorPlot = 476 // the widest plot that still hides minor ticks: a 519px frame less its 2.75rem value axis, in pixels
)

// Span is a stretch of days drawn behind the columns and bracketed above them,
// such as a campaign: From and To are its first and last shop day, Label its name.
type Span struct {
	From, To time.Time
	Label    string
}

// Column is what one drawn column holds: a day, or Days days from Day on.
type Column struct {
	Day   time.Time
	Days  int
	Value int64
}

// Grouped is whether the series is too long to draw a column a day: it is drawn
// a column to each run of seven days, counted back from the last.
func (s Series) Grouped() bool { return len(s.Buckets) > maxDailyColumns }

// Columns is the series as drawn, which is its days, or runs of them when
// Grouped. The earliest run holds what is left, so it can be shorter.
func (s Series) Columns() []Column {
	n := len(s.Buckets)
	if !s.Grouped() {
		cols := make([]Column, n)
		for i, b := range s.Buckets {
			cols[i] = Column{Day: b.Day, Days: 1, Value: b.Value}
		}
		return cols
	}
	var cols []Column
	for end := n; end > 0; {
		start := max(end-daysPerColumn, 0)
		c := Column{Day: s.Buckets[start].Day, Days: end - start}
		for _, b := range s.Buckets[start:end] {
			c.Value += b.Value
		}
		cols = append([]Column{c}, cols...)
		end = start
	}
	return cols
}

// Peaks are the columns tied for the highest value, none if that is zero.
func Peaks(cols []Column) []Column {
	var top int64
	for _, c := range cols {
		top = max(top, c.Value)
	}
	var peaks []Column
	for _, c := range cols {
		if top > 0 && c.Value == top {
			peaks = append(peaks, c)
		}
	}
	return peaks
}

// ColumnsProps is a series drawn as columns, with the spans that overlap it.
// Series.Label heads the table's value column. Caption and Note are the page's
// sentences, already localised; so are the table's heads and PartialLabel,
// which says what the last row is counted up to when Series.Partial.
//
// Previous is how many days from the first are the stretch the spans are
// compared with, which is not a stored period: they are drawn in the previous
// colour under a line named PreviousLabel, and the table names them too.
// It has no effect on a series drawn in runs of days.
type ColumnsProps struct {
	Series                                Series
	Spans                                 []Span
	Previous                              int
	PreviousLabel                         string
	Caption, Note                         string
	DayHeading, SpanHeading, PartialLabel string
}

type bar struct {
	X, Width  string
	Y, Height float64
	Open      bool
	Previous  bool
}

type valueLabel struct {
	X, Text string
	Y       float64
}

// strip is a campaign over the columns: the ground behind them and, in a lane,
// a bracket under its name. The bracket is open at an end that is not on the
// chart: a campaign that began before the first day, or ends after the last.
type strip struct {
	X, Width, RightX    string
	Y                   float64 // the bracket's line
	Window              bool    // a line under its name, not a bracket: it is not a stored period
	Hidden              bool    // no lane was free: the ground only, and the table names it
	OpenLeft, OpenRight bool
	Name, NameX, Anchor string
	NameY               float64
}

type columnRow struct {
	Heading, Value, Spans string
}

// columns is everything Columns draws, worked out.
type columns struct {
	Height           int
	Baseline, LabelY float64
	Plain            bool // three to six days with values: no axis, every bar labelled
	Grid             []gridLine
	Bars             []bar
	Strips           []strip
	Values           []valueLabel
	Ticks            []dayTick
	TodayX           string
	Rows             []columnRow
	Hits             []hit
	HasSpans         bool
	Note             string
}

// barShare is the part of its column a bar fills, at most 24px at the widest.
func barShare(n int) float64 {
	return math.Min(0.62, 24/(float64(desktopPlot)/float64(n)))
}

func dayIndex(from, to time.Time) int {
	return int(math.Round(to.Sub(from).Hours() / 24))
}

// plan is a span in column units: a and b are where it begins and ends, as
// fractions of a column where a column holds several days.
type plan struct {
	a, b         float64
	first, last  time.Time // the days of it that are drawn
	early        bool      // it began before the first day drawn
	runs         bool      // it goes on after the last day drawn
	label, short string
	window       bool
}

func place(cols []Column, day time.Time) (index, days int) {
	for i, c := range cols {
		if !day.Before(c.Day) && day.Before(c.Day.AddDate(0, 0, c.Days)) {
			return i, c.Days
		}
	}
	return -1, 1
}

func plans(ctx context.Context, cols []Column, first, last time.Time, spans []Span) []plan {
	var out []plan
	for _, s := range spans {
		from, to := s.From, s.To
		if from.Before(first) {
			from = first
		}
		if to.After(last) {
			to = last
		}
		if from.After(to) {
			continue
		}
		i0, l0 := place(cols, from)
		i1, l1 := place(cols, to)
		p := plan{
			a:     float64(i0) + float64(dayIndex(cols[i0].Day, from))/float64(l0),
			b:     float64(i1) + float64(dayIndex(cols[i1].Day, to)+1)/float64(l1),
			first: from, last: to, early: s.From.Before(first), runs: s.To.After(last),
			label: s.Label, short: s.Label,
		}
		if p.runs {
			p.label = fmt.Sprintf(i18n.T(ctx, i18n.KeyChartSpanUntil), s.Label, axisDay(ctx, s.To))
		}
		out = append(out, p)
	}
	return out
}

// textWidth is a 12px label's width in pixels, estimated high: a Han character
// is 12px wide, anything else 6.8px.
func textWidth(s string) float64 {
	var w float64
	for _, r := range s {
		if r > 0x2e80 {
			w += 12
		} else {
			w += 6.8
		}
	}
	return w
}

// nameAt is where a strip's name goes so that it stays inside the plot at its
// narrowest, and what it says. A name starts at its strip, or ends at the
// strip's end when it would cross the right edge, or, if that does not fit
// either, starts at the plot's left edge. A name wider than the plot drops the
// day it ends on, then is cut. It returns the text, the column it is placed at,
// its anchor, and the left and right it takes, in pixels.
func nameAt(p plan, n int) (text string, at float64, anchor string, left, right float64) {
	text = p.label
	if textWidth(text) > narrowPlot {
		text = p.short
	}
	for textWidth(text) > narrowPlot {
		r := []rune(text)
		text = string(r[:max(len(r)-2, 0)]) + "…"
	}
	w, col := textWidth(text), float64(narrowPlot)/float64(n)
	a, e := p.a*col, p.b*col
	switch {
	case a+w <= narrowPlot:
		return text, p.a, "start", a, a + w
	case w <= e:
		return text, p.b, "end", e - w, e
	}
	return text, 0, "start", 0, w
}

func newColumns(ctx context.Context, p ColumnsProps) columns {
	cols := p.Series.Columns()
	n := len(cols)
	buckets := p.Series.Buckets
	first, last := buckets[0].Day, buckets[len(buckets)-1].Day
	full := p.Series.Density() == DensityFull
	band := 100 / float64(n)
	grouped := p.Series.Grouped()

	pl := plans(ctx, cols, first, last, p.Spans)
	if p.Previous > 0 && !grouped {
		through := first.AddDate(0, 0, min(p.Previous, len(buckets))-1)
		window := plans(ctx, cols, first, last, []Span{{From: first, To: through, Label: p.PreviousLabel}})
		window[0].window = true
		pl = append(window, pl...)
	}
	lanes, used := assignLanes(pl, n)

	r := columns{Plain: !full, HasSpans: len(pl) > 0}
	scale := columnsScale(cols, full)
	top := float64(valueRoom)
	if used > 0 {
		top = valueGap(cols, scale) + float64(laneTop+laneHeight*used)
	}
	r.Baseline = top + columnsPlot
	r.Height = int(r.Baseline) + axisBand
	r.LabelY = r.Baseline + axisBand - 8

	r.Grid = columnsAxis(ctx, scale, full, r.Baseline)
	previous := p.Previous
	if grouped {
		previous = 0
	}
	r.Bars, r.Values = barsOf(cols, scale, r.Baseline, full, p.Series.Partial, previous)
	if p.Series.Partial {
		lastDays := float64(cols[n-1].Days)
		r.TodayX = percent((float64(n-1) + (lastDays-0.5)/lastDays) * band)
	}
	r.Strips = stripsOf(pl, lanes, n)
	r.Ticks = columnTicks(ctx, cols, grouped, p.Series.Partial, band)
	r.Rows = tableRows(ctx, p, cols, pl)
	r.Hits = make([]hit, n)
	for i := range r.Hits {
		r.Hits[i] = hit{X: percent(float64(i) * band), Width: percent(band)}
	}

	sentences := make([]string, 0, 3)
	if grouped && cols[0].Days != daysPerColumn {
		sentences = append(sentences, i18n.Count(ctx, i18n.KeyChartShortFirst, int64(cols[0].Days), cols[0].Days))
	}
	if p.Note != "" {
		sentences = append(sentences, p.Note)
	}
	hidden := 0
	for _, l := range lanes {
		if l < 0 {
			hidden++
		}
	}
	if hidden > 0 {
		sentences = append(sentences, i18n.Count(ctx, i18n.KeyChartSpansUnbracketed, int64(hidden), hidden))
	}
	r.Note = strings.TrimSpace(strings.Join(sentences, " "))
	return r
}

// assignLanes puts each plan in the first lane, counting from the top, that is
// free where it begins, and returns the lane of each, -1 for one that finds none
// of the maxLanes, and how many lanes are used. Plans are taken by the day they
// begin, so the lanes read top to bottom as the table's rows do. A lane is taken
// from where a bracket or its name starts to where either ends, with 8px to
// spare, so two plans that overlap in time never share one.
func assignLanes(pl []plan, n int) (lanes []int, used int) {
	order := make([]int, len(pl))
	for k := range order {
		order[k] = k
	}
	slices.SortStableFunc(order, func(x, y int) int { return cmp.Compare(pl[x].a, pl[y].a) })

	col := float64(narrowPlot) / float64(n)
	var ends [maxLanes]float64
	for l := range ends {
		ends[l] = math.Inf(-1)
	}
	lanes = make([]int, len(pl))
	for _, k := range order {
		_, _, _, nameLeft, nameRight := nameAt(pl[k], n)
		left, right := min(pl[k].a*col, nameLeft), max(pl[k].b*col, nameRight)
		if pl[k].window {
			left, right = nameLeft, nameRight // its line has no ends, so a bracket may begin where it stops
		}
		lanes[k] = -1
		for l := range ends {
			if ends[l]+8 < left {
				ends[l], lanes[k] = right, l
				used = max(used, l+1)
				break
			}
		}
	}
	return lanes, used
}

// columnsScale is the value the top of the value axis stands for. Fewer than
// seven days with values have the highest bar fill the area.
func columnsScale(cols []Column, full bool) int64 {
	var scale int64
	for _, c := range cols {
		scale = max(scale, c.Value)
	}
	if !full {
		return scale
	}
	step := axisStep(scale, MeasureCount)
	return step * gridLines(scale, step)
}

// valueGap is the room between the last lane and the columns: enough for the
// value over the highest bar, less what that bar already leaves under the top of
// the area.
func valueGap(cols []Column, scale int64) float64 {
	if scale <= 0 {
		return valueRoom
	}
	var highest int64
	for _, c := range cols {
		highest = max(highest, c.Value)
	}
	return max(minValueRoom, valueRoom-columnsPlot*(1-float64(highest)/float64(scale)))
}

// columnsAxis is the lines of the value axis, which tops out at scale. Fewer
// than seven days with values have the baseline alone.
func columnsAxis(ctx context.Context, scale int64, full bool, baseline float64) []gridLine {
	if !full {
		return []gridLine{{Y: baseline, Baseline: true}}
	}
	step := axisStep(scale, MeasureCount)
	lines := gridLines(scale, step)
	divisor, suffix := i18n.AxisUnit(ctx, scale)
	grid := make([]gridLine, 0, lines+1)
	for k := range lines + 1 {
		v := k * step
		grid = append(grid, gridLine{
			Y:     baseline - float64(v)/float64(scale)*columnsPlot,
			Label: MeasureCount.axisText(v, divisor, suffix), Baseline: k == 0,
		})
	}
	return grid
}

// barsOf is a bar for each column with a value, and the value over it: over
// every bar when few are drawn, else over the highest, the ones tied with it,
// and today.
func barsOf(cols []Column, scale int64, baseline float64, full, partial bool, previous int) ([]bar, []valueLabel) {
	n := len(cols)
	band := 100 / float64(n)
	share := barShare(n)
	var peak int64
	for _, c := range cols {
		peak = max(peak, c.Value)
	}
	var bars []bar
	var values []valueLabel
	for i, c := range cols {
		if c.Value <= 0 {
			continue
		}
		h := math.Max(2, float64(c.Value)/float64(scale)*columnsPlot)
		bars = append(bars, bar{
			X: percent(float64(i)*band + (1-share)/2*band), Width: percent(share * band),
			Y: baseline - h, Height: h, Open: partial && i == n-1,
			Previous: i < previous,
		})
		if !full || c.Value == peak || i == n-1 {
			values = append(values, valueLabel{
				X: percent((float64(i) + 0.5) * band), Y: baseline - h - 6, Text: MeasureCount.text(c.Value),
			})
		}
	}
	return bars, values
}

func stripsOf(pl []plan, lanes []int, n int) []strip {
	band := 100 / float64(n)
	strips := make([]strip, 0, len(pl))
	for k, sp := range pl {
		s := strip{
			X: percent(sp.a * band), Width: percent((sp.b - sp.a) * band), RightX: percent(sp.b * band),
			Window: sp.window, Hidden: lanes[k] < 0, OpenLeft: sp.early, OpenRight: sp.runs,
		}
		if !s.Hidden {
			text, at, anchor, _, _ := nameAt(sp, n)
			top := float64(laneTop + laneHeight*lanes[k])
			s.Y, s.NameY = top+laneBracketAt, top+laneNameBase
			s.Name, s.NameX, s.Anchor = text, percent(at*band), anchor
		}
		strips = append(strips, s)
	}
	return strips
}

// tableRows is the table: each column's day, its value, and the campaigns that
// cover it, by name.
func tableRows(ctx context.Context, p ColumnsProps, cols []Column, pl []plan) []columnRow {
	n := len(cols)
	names := make([][]string, n)
	for _, sp := range pl {
		i0, _ := place(cols, sp.first)
		i1, _ := place(cols, sp.last)
		for i := i0; i <= i1; i++ {
			label := sp.label
			if days := coveredDays(ctx, cols[i], sp.first, sp.last); days != "" {
				label = fmt.Sprintf(i18n.T(ctx, i18n.KeyChartQualified), label, days)
			}
			names[i] = append(names[i], label)
		}
	}
	separator := i18n.T(ctx, i18n.KeyChartListSeparator)
	grouped := p.Series.Grouped()
	rows := make([]columnRow, 0, n)
	for i, c := range cols {
		heading := axisDay(ctx, c.Day)
		if grouped && c.Days != daysPerColumn {
			heading = fmt.Sprintf(i18n.T(ctx, i18n.KeyChartQualified), heading, i18n.Count(ctx, i18n.KeyChartColumnDays, int64(c.Days), c.Days))
		}
		if i == n-1 && p.Series.Partial && p.PartialLabel != "" {
			heading = fmt.Sprintf(i18n.T(ctx, i18n.KeyChartQualified), heading, p.PartialLabel)
		}
		rows = append(rows, columnRow{Heading: heading, Value: MeasureCount.text(c.Value), Spans: strings.Join(names[i], separator)})
	}
	return rows
}

// coveredDays names the days of a column of several that a span between first
// and last covers, or nothing when it covers them all.
func coveredDays(ctx context.Context, c Column, first, last time.Time) string {
	if c.Days <= 1 {
		return ""
	}
	end := c.Day.AddDate(0, 0, c.Days-1)
	lo, hi := c.Day, end
	if first.After(lo) {
		lo = first
	}
	if last.Before(hi) {
		hi = last
	}
	if lo.Equal(c.Day) && hi.Equal(end) {
		return ""
	}
	days := axisDay(ctx, lo)
	if !lo.Equal(hi) {
		days += "–" + axisDay(ctx, hi)
	}
	return days
}

// minorTick is whether the tick of column i of n gives way on a narrow chart:
// every third of a chart in runs, every other of a week.
func minorTick(i, n int, grouped bool) bool {
	switch {
	case grouped:
		return (n-1-i)%3 != 0
	case n <= 7:
		return (n-1-i)%2 == 1
	}
	return false
}

// nearToday says what becomes of the day tick of column i of n, labelled day,
// when "Today" ends the axis: it is dropped when even the widest plot that hides
// minor ticks leaves its label less than 4px from "Today", and it is minor, so a
// narrow chart drops it, when the narrowest plot does.
func nearToday(day, today string, i, n int) (drop, minor bool) {
	need := textWidth(day)/2 + textWidth(today) + 4
	left := 1 - (float64(i)+0.5)/float64(n)
	return left*widestMinorPlot < need, left*narrowPlot < need
}

// columnTicks labels the axis: a long chart, drawn in runs, labels the day each
// starts on; otherwise the days tickDays picks, and every day of a week, and
// today at the end when the last day is still going, which a day tick must not
// meet.
func columnTicks(ctx context.Context, cols []Column, grouped, today bool, band float64) []dayTick {
	n := len(cols)
	picked := map[int]bool{}
	if !grouped {
		days := make([]time.Time, n)
		for i, c := range cols {
			days[i] = c.Day
		}
		for _, i := range tickDays(days) {
			picked[i] = true
		}
		picked[0] = n <= 7
	}
	ticks := make([]dayTick, 0, n)
	for i, c := range cols {
		if !grouped && !picked[i] && i != n-1 {
			continue
		}
		t := dayTick{X: percent((float64(i) + 0.5) * band), Anchor: "middle", Label: axisDay(ctx, c.Day)}
		t.Minor = minorTick(i, n, grouped)
		if !grouped && today && n > 7 && i != n-1 {
			var drop bool
			if drop, t.Minor = nearToday(t.Label, i18n.T(ctx, i18n.KeyChartToday), i, n); drop {
				continue
			}
		}
		if i == n-1 {
			endTick(ctx, &t, n, !grouped && today)
		}
		ticks = append(ticks, t)
	}
	return ticks
}

// endTick makes t the last tick of an axis of n ticks: "Today" when the last
// day is still going, and flush with the right edge when there are many.
func endTick(ctx context.Context, t *dayTick, n int, today bool) {
	if today {
		t.Label, t.Today = i18n.T(ctx, i18n.KeyChartToday), true
	}
	if n > 7 {
		t.X, t.Anchor = "100%", "end"
	}
}
