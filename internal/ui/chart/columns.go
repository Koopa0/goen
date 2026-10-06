package chart

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/i18n"
)

const (
	maxDailyColumns = 45  // beyond it, a column holds daysPerColumn days
	daysPerColumn   = 7   // counted back from the last day
	columnsPlot     = 150 // the value area of the columns, in pixels
	stripRow        = 18  // the height of a row of campaign strips
	narrowPlot      = 244 // the narrowest a plot gets, in pixels, which the labels on it are fitted to
	desktopPlot     = 888 // the width of a plot at its widest, in pixels
	thinGapsFrom    = 15  // from this many columns a gap between days is 1px, not 2px
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
type ColumnsProps struct {
	Series                                Series
	Spans                                 []Span
	Caption, Note                         string
	DayHeading, SpanHeading, PartialLabel string
}

type bar struct {
	X, Width  string
	Y, Height float64
	Open      bool
}

type valueLabel struct {
	X, Text string
	Y       float64
}

type strip struct {
	GroundX, GroundWidth string
	X, Width             string
	Y                    float64
	Gaps                 []string
	Thin                 bool
	TodayX               string
	Name, NameX, Anchor  string
	NameY                float64
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

// taken is the room a strip's name takes, in pixels, on one row of names.
type taken struct {
	left, right float64
	row         int
}

// clashes is whether a name between left and right runs into one already placed
// on row, with 8px to spare.
func clashes(placed []taken, row int, left, right float64) bool {
	for _, o := range placed {
		if o.row == row && left < o.right+8 && o.left < right+8 {
			return true
		}
	}
	return false
}

// plan is a span in column units: a and b are where it begins and ends, as
// fractions of a column where a column holds several days; end is where its
// strip stops, which is the middle of the last day while the span runs on.
type plan struct {
	a, b, end    float64
	first, last  time.Time // the days of it that are drawn
	runs         bool      // it goes on after the last day drawn
	label, short string
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
			first: from, last: to, runs: s.To.After(last),
			label: s.Label, short: s.Label,
		}
		p.end = p.b
		if p.runs {
			p.end = float64(i1) + (float64(dayIndex(cols[i1].Day, to))+0.5)/float64(l1)
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
// narrowest, and what it says. A name that is running ends at its strip's end;
// any other starts at its strip, or ends at the strip's end when it would start
// past 55% of the plot or cross the right edge, or, if that does not fit
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
	a, e := p.a*col, p.end*col
	switch {
	case !p.runs && p.a/float64(n) <= 0.55 && a+w <= narrowPlot:
		return text, p.a, "start", a, a + w
	case w <= e:
		return text, p.end, "end", e - w, e
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
	rows, deepest := stripRows(pl, n)

	r := columns{Plain: !full, HasSpans: len(pl) > 0}
	top := 22.0 // room for a value above the highest bar
	if len(pl) > 0 {
		top = float64(34 + stripRow*deepest)
	}
	r.Baseline = top + columnsPlot
	r.Height = int(r.Baseline) + axisBand
	r.LabelY = r.Baseline + axisBand - 8

	var scale int64
	r.Grid, scale = columnsAxis(ctx, cols, full, r.Baseline)
	r.Bars, r.Values = barsOf(cols, scale, r.Baseline, full, p.Series.Partial)
	if p.Series.Partial {
		lastDays := float64(cols[n-1].Days)
		r.TodayX = percent((float64(n-1) + (lastDays-0.5)/lastDays) * band)
	}
	r.Strips = stripsOf(pl, rows, top, n, grouped)
	r.Ticks = columnTicks(ctx, cols, grouped, band)
	r.Rows = tableRows(ctx, p, cols, pl)
	for i := range n {
		r.Hits = append(r.Hits, hit{X: percent(float64(i) * band), Width: percent(band)})
	}

	r.Note = p.Note
	if grouped && cols[0].Days != daysPerColumn {
		short := i18n.Count(ctx, i18n.KeyChartShortFirst, int64(cols[0].Days), cols[0].Days)
		r.Note = strings.TrimSpace(short + " " + p.Note)
	}
	return r
}

// stripRows puts each strip's name on the lowest row where it does not run into
// the names already placed, and returns the rows and the deepest one used.
func stripRows(pl []plan, n int) (rows []int, deepest int) {
	rows = make([]int, len(pl))
	placed := make([]taken, 0, len(pl))
	for k, sp := range pl {
		_, _, _, left, right := nameAt(sp, n)
		row := 0
		for clashes(placed, row, left, right) {
			row++
		}
		rows[k], deepest = row, max(deepest, row)
		placed = append(placed, taken{left, right, row})
	}
	return rows, deepest
}

// columnsAxis is the lines of the value axis and the value its top stands for.
// Fewer than seven days with values have the baseline alone, and the highest
// bar fills the area.
func columnsAxis(ctx context.Context, cols []Column, full bool, baseline float64) (grid []gridLine, scale int64) {
	for _, c := range cols {
		scale = max(scale, c.Value)
	}
	if !full {
		return []gridLine{{Y: baseline, Baseline: true}}, scale
	}
	step := axisStep(scale, MeasureCount)
	lines := gridLines(scale, step)
	scale = step * lines
	divisor, suffix := i18n.AxisUnit(ctx, scale)
	for k := range lines + 1 {
		v := k * step
		grid = append(grid, gridLine{
			Y:     baseline - float64(v)/float64(scale)*columnsPlot,
			Label: MeasureCount.axisText(v, divisor, suffix), Baseline: k == 0,
		})
	}
	return grid, scale
}

// barsOf is a bar for each column with a value, and the value over it: over
// every bar when few are drawn, else over the highest, the ones tied with it,
// and today.
func barsOf(cols []Column, scale int64, baseline float64, full, partial bool) ([]bar, []valueLabel) {
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
		})
		if !full || c.Value == peak || i == n-1 {
			values = append(values, valueLabel{
				X: percent((float64(i) + 0.5) * band), Y: baseline - h - 6, Text: MeasureCount.text(c.Value),
			})
		}
	}
	return bars, values
}

func stripsOf(pl []plan, rows []int, top float64, n int, grouped bool) []strip {
	band := 100 / float64(n)
	strips := make([]strip, 0, len(pl))
	for k, sp := range pl {
		text, at, anchor, _, _ := nameAt(sp, n)
		s := strip{
			GroundX: percent(sp.a * band), GroundWidth: percent((sp.b - sp.a) * band),
			X: percent(sp.a * band), Width: percent((sp.end - sp.a) * band),
			Y: top - 12 - float64(stripRow*rows[k]), Thin: n >= thinGapsFrom,
			Name: text, NameX: percent(at * band), Anchor: anchor,
		}
		s.NameY = s.Y - 6
		if !grouped {
			for i := int(sp.a) + 1; float64(i) < math.Ceil(sp.end); i++ {
				s.Gaps = append(s.Gaps, percent(float64(i)*band))
			}
		}
		if sp.runs {
			s.TodayX = percent(sp.end * band)
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

// columnTicks labels the axis: a long chart, drawn in runs, labels the day each
// starts on; otherwise the days tickDays picks, and every day of a week, and
// today at the end.
func columnTicks(ctx context.Context, cols []Column, grouped bool, band float64) []dayTick {
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
		switch {
		case grouped:
			t.Minor = (n-1-i)%3 != 0
		case n <= 7:
			t.Minor = (n-1-i)%2 == 1
		}
		if i == n-1 {
			if !grouped {
				t.Label, t.Today = i18n.T(ctx, i18n.KeyChartToday), true
			}
			if n > 7 {
				t.X, t.Anchor = "100%", "end"
			}
		}
		ticks = append(ticks, t)
	}
	return ticks
}
