package chart

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
)

func renderColumns(t *testing.T, locale i18n.Locale, p ColumnsProps) string {
	t.Helper()
	var b bytes.Buffer
	if err := Columns(p).Render(i18n.WithLocale(t.Context(), locale), &b); err != nil {
		t.Fatalf("Columns.Render: %v", err)
	}
	return b.String()
}

func columnsProps(buckets []Bucket) ColumnsProps {
	return ColumnsProps{
		Series:     Series{Label: "Paid orders", Buckets: buckets, Partial: true},
		Caption:    "Paid orders came in on some days.",
		Note:       "Counted up to 15:20.",
		DayHeading: "Date", SpanHeading: "Campaign", PartialLabel: "up to 15:20",
	}
}

// valued is n days from 2026-09-07 with the given values at their indexes.
func valued(n int, at map[int]int64) []Bucket {
	buckets := days(n)
	for i, v := range at {
		buckets[i].Value = v
	}
	return buckets
}

func TestColumnsGroupLongSeriesBySevenDaysBackFromTheLast(t *testing.T) {
	t.Parallel()

	s := Series{Buckets: valued(90, map[int]int64{0: 5, 5: 1, 6: 2, 89: 4})}
	if !s.Grouped() {
		t.Fatal("90 days are not Grouped, want grouped")
	}
	cols := s.Columns()
	if len(cols) != 13 {
		t.Fatalf("90 days make %d columns, want 13", len(cols))
	}
	if first := cols[0]; first.Days != 6 || first.Value != 6 || !first.Day.Equal(s.Buckets[0].Day) {
		t.Errorf("earliest column = %+v, want the 6 days left over from the first, holding 6", first)
	}
	if last := cols[12]; last.Days != 7 || last.Value != 4 || !last.Day.Equal(s.Buckets[83].Day) {
		t.Errorf("latest column = %+v, want the 7 days ending on the last, from day 84, holding 4", last)
	}
	var held, sum int
	for _, c := range cols {
		held += c.Days
		sum += int(c.Value)
	}
	if held != 90 || sum != 12 {
		t.Errorf("columns hold %d days and %d orders, want 90 and 12", held, sum)
	}
	if got := (Series{Buckets: days(45)}).Columns(); len(got) != 45 {
		t.Errorf("45 days make %d columns, want a column a day", len(got))
	}
}

func TestPeaksAreEveryColumnTiedForTheMost(t *testing.T) {
	t.Parallel()

	cols := Series{Buckets: valued(10, map[int]int64{1: 9, 4: 3, 8: 9})}.Columns()
	if got := Peaks(cols); len(got) != 2 || got[0].Value != 9 || got[1].Value != 9 {
		t.Errorf("Peaks = %+v, want both days of 9", got)
	}
	if got := Peaks(Series{Buckets: days(10)}.Columns()); got != nil {
		t.Errorf("Peaks of nothing = %+v, want none", got)
	}
}

func TestColumnsDrawsOnlyFromThreeDaysWithOrders(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		at   map[int]int64
		want bool
	}{
		{"none", nil, false},
		{"two", map[int]int64{0: 1, 29: 2}, false},
		{"three", map[int]int64{0: 1, 10: 2, 29: 2}, true},
	} {
		got := renderColumns(t, i18n.En, columnsProps(valued(30, tc.at)))
		if drawn := strings.Contains(got, "<svg"); drawn != tc.want {
			t.Errorf("%s: drawn = %v, want %v", tc.name, drawn, tc.want)
		}
		if !tc.want && got != "" {
			t.Errorf("%s: Columns = %q, want nothing, the page says it in a sentence", tc.name, got)
		}
	}
}

func TestColumnsHaveNoBarOnADayWithoutOrders(t *testing.T) {
	t.Parallel()

	got := renderColumns(t, i18n.En, columnsProps(valued(30, map[int]int64{3: 2, 4: 5, 20: 1, 29: 3})))
	if bars := strings.Count(got, `class="goen-chart__hue`); bars != 4 {
		t.Errorf("%d bars for 4 days with orders, want 4: a day without orders has none", bars)
	}
}

func TestColumnsWithFewDaysLabelEveryBarAndDrawNoAxis(t *testing.T) {
	t.Parallel()

	got := renderColumns(t, i18n.En, columnsProps(valued(14, map[int]int64{1: 2, 5: 7, 9: 4, 13: 1})))
	if strings.Contains(got, "goen-chart__yaxis") || strings.Contains(got, `class="goen-chart__grid"`) {
		t.Error("three to six days draw an axis or grid lines, want none")
	}
	for _, v := range []string{">2<", ">7<", ">4<", ">1<"} {
		if !strings.Contains(got, `class="goen-chart__value"`) || !strings.Contains(got, v) {
			t.Errorf("a bar of %s is not labelled", v)
		}
	}
	if n := strings.Count(got, `class="goen-chart__value"`); n != 4 {
		t.Errorf("%d bar labels, want 4", n)
	}
}

func TestColumnsWithSevenDaysLabelOnlyTheHighestAndToday(t *testing.T) {
	t.Parallel()

	at := map[int]int64{}
	for i := range 8 {
		at[i*3] = int64(i + 1)
	}
	at[21], at[29] = 9, 4 // 9 is the highest; day 29 is today
	got := renderColumns(t, i18n.En, columnsProps(valued(30, at)))
	if !strings.Contains(got, "goen-chart__yaxis") {
		t.Error("seven days with orders draw no axis")
	}
	if n := strings.Count(got, `class="goen-chart__value"`); n != 2 {
		t.Errorf("%d bar labels, want 2: the highest and today\n%s", n, got)
	}

	// Tied for the highest: every one is labelled, never just the first.
	at[12] = 9
	got = renderColumns(t, i18n.En, columnsProps(valued(30, at)))
	if n := strings.Count(got, `class="goen-chart__value"`); n != 3 {
		t.Errorf("%d bar labels with two days tied for the highest, want 3 (both and today)", n)
	}
}

func TestColumnsDrawTodayOpenWithItsMark(t *testing.T) {
	t.Parallel()

	p := columnsProps(valued(30, map[int]int64{3: 2, 4: 5, 20: 1, 29: 3}))
	got := renderColumns(t, i18n.En, p)
	if n := strings.Count(got, "goen-chart__open"); n != 1 {
		t.Errorf("%d open bars, want today's alone", n)
	}
	if !strings.Contains(got, `class="goen-chart__todaymark"`) {
		t.Error("today has no mark under its column")
	}
	if !strings.Contains(got, `<th scope="row">Oct 6 (up to 15:20)</th>`) {
		t.Errorf("the table does not say that today is counted up to 15:20:\n%s", got)
	}

	p.Series.Partial = false
	if got := renderColumns(t, i18n.En, p); strings.Contains(got, "goen-chart__open") || strings.Contains(got, "goen-chart__todaymark") {
		t.Error("a whole last day is drawn as unfinished")
	}
}

func TestColumnsAreHiddenFromAssistiveTechnologyAndSetNoStyle(t *testing.T) {
	t.Parallel()

	got := renderColumns(t, i18n.En, columnsProps(valued(30, map[int]int64{3: 2, 4: 5, 20: 1, 29: 3})))
	if strings.Contains(got, "style=") {
		t.Error("Columns sets a style attribute, which the content security policy refuses")
	}
	if outer, hidden := strings.Count(got, `<svg class="goen-chart__`), strings.Count(got, `aria-hidden="true" focusable="false"`); outer != hidden {
		t.Errorf("%d outer SVGs and %d hidden from assistive technology, want all", outer, hidden)
	}
	for _, want := range []string{
		`<figcaption class="goen-chart__caption">Paid orders came in on some days.</figcaption>`,
		"<summary>Show as a table</summary>",
		`<p class="goen-chart__note">Counted up to 15:20.</p>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Columns does not contain %s", want)
		}
	}
}

// campaign is a Span between two of the series' days, counted from 2026-09-07.
func campaign(label string, from, to int) Span {
	day := func(i int) time.Time { return time.Date(2026, 9, 7+i, 0, 0, 0, 0, time.UTC) }
	return Span{From: day(from), To: day(to), Label: label}
}

func TestColumnsBracketACampaignAndTheTableNamesItOnEachOfItsDays(t *testing.T) {
	t.Parallel()

	p := columnsProps(valued(30, map[int]int64{3: 2, 4: 5, 20: 1, 29: 3}))
	p.Spans = []Span{campaign("Tea week", 3, 5)}
	got := renderColumns(t, i18n.En, p)

	for _, want := range []string{
		`class="goen-chart__strip"`, `class="goen-chart__span"`,
		`class="goen-chart__spanlabel"`, ">Tea week<",
		`<th scope="col" class="goen-chart__spans" data-readout="note">Campaign</th>`,
		`<th scope="row">Sep 10</th><td>2</td><td class="goen-chart__spans">Tea week</td>`,
		`<th scope="row">Sep 12</th><td>0</td><td class="goen-chart__spans">Tea week</td>`,
		`<th scope="row">Sep 13</th><td>0</td><td class="goen-chart__spans"></td>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Columns does not contain %s\n%s", want, got)
		}
	}
	if n := strings.Count(got, `class="goen-chart__strip"`); n != 1 {
		t.Errorf("%d bracket lines, want 1 across the 3 days", n)
	}
	if n := strings.Count(got, `class="goen-chart__strip-end"`); n != 2 {
		t.Errorf("%d bracket ends, want one at each end of a campaign that began and ended in the chart", n)
	}
	if strings.Contains(got, `class="goen-chart__todaymark" x1=`) && strings.Count(got, "goen-chart__todaymark") != 1 {
		t.Error("a campaign that ended is marked with today")
	}

	p.Spans = nil
	if got := renderColumns(t, i18n.En, p); strings.Contains(got, "goen-chart__strip") || strings.Contains(got, "Campaign") {
		t.Error("a period without a campaign draws a strip or a campaign column")
	}
}

func TestColumnsEndARunningCampaignAtTodayAndSayWhenItEnds(t *testing.T) {
	t.Parallel()

	p := columnsProps(valued(30, map[int]int64{3: 2, 4: 5, 20: 1, 29: 3}))
	p.Spans = []Span{campaign("Autumn picks", 20, 40)} // runs on past the last day, 29
	got := renderColumns(t, i18n.En, p)

	if n := strings.Count(got, `class="goen-chart__todaymark"`); n != 1 {
		t.Errorf("%d today marks, want the one under today's column and none across the bracket", n)
	}
	if n := strings.Count(got, `class="goen-chart__strip-end"`); n != 1 {
		t.Errorf("%d bracket ends on a campaign that has not ended, want its start alone: an open end says it goes on", n)
	}
	for _, want := range []string{">Autumn picks, until Oct 17<", `<td class="goen-chart__spans">Autumn picks, until Oct 17</td>`} {
		if !strings.Contains(got, want) {
			t.Errorf("Columns does not contain %s\n%s", want, got)
		}
	}
	zh := renderColumns(t, i18n.ZhHant, p)
	if !strings.Contains(zh, ">Autumn picks，至 10/17<") {
		t.Errorf("the Chinese strip does not say when it ends:\n%s", zh)
	}
}

func TestColumnsOfSevenDaysSayWhichDaysACampaignCoversOfAColumn(t *testing.T) {
	t.Parallel()

	// The earliest column holds the first six days, the next days 6 to 12, and
	// the one after that days 13 to 19.
	p := columnsProps(valued(90, map[int]int64{0: 1, 10: 2, 50: 3, 60: 1, 89: 4}))
	p.Spans = []Span{campaign("Tea week", 10, 16), campaign("Whole column", 6, 12)}
	got := renderColumns(t, i18n.En, p)
	for _, want := range []string{
		`<th scope="row">Sep 13</th><td>2</td><td class="goen-chart__spans">Tea week (Sep 17–Sep 19), Whole column</td>`,
		`<th scope="row">Sep 20</th><td>0</td><td class="goen-chart__spans">Tea week (Sep 20–Sep 23)</td>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the table does not contain %s\n%s", want, got)
		}
	}
}

func TestColumnsOfSevenDaysNameTheShortEarliestColumnUnderTheChartAndInTheTable(t *testing.T) {
	t.Parallel()

	got := renderColumns(t, i18n.En, columnsProps(valued(90, map[int]int64{0: 1, 10: 2, 50: 3, 60: 1, 89: 4})))
	for _, want := range []string{
		`<p class="goen-chart__note">The earliest stretch has 6 days. Counted up to 15:20.</p>`,
		`<th scope="row">Sep 7 (6 days)</th>`,
		`<th scope="row">Sep 13</th>`,
		`<th scope="row">Nov 29 (up to 15:20)</th>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Columns does not contain %s", want)
		}
	}
	if whole := renderColumns(t, i18n.En, columnsProps(valued(91, map[int]int64{0: 1, 10: 2, 50: 3}))); strings.Contains(whole, "earliest stretch") {
		t.Error("91 days are 13 whole columns, but the earliest is said to be short")
	}
}

func TestColumnTicksLabelTheMondaysAndToday(t *testing.T) {
	t.Parallel()

	labels := func(n int) []string {
		ticks := columnTicks(i18n.WithLocale(t.Context(), i18n.En), Series{Buckets: days(n)}.Columns(), false, true, 100/float64(n))
		out := make([]string, 0, len(ticks))
		for _, tk := range ticks {
			out = append(out, tk.Label)
		}
		return out
	}
	// The first of 30 days is a Monday, and so is the 29th, which sits too near
	// the end to be labelled beside "Today".
	if got, want := labels(30), []string{"Sep 14", "Sep 21", "Sep 28", "Today"}; !slices.Equal(got, want) {
		t.Errorf("30 days: ticks %v, want %v", got, want)
	}
	if got := labels(7); len(got) != 7 || got[6] != "Today" {
		t.Errorf("7 days: ticks %v, want a label for each, the last Today", got)
	}
}

func TestColumnTicksKeepTheDaysNearTodayClearOfIt(t *testing.T) {
	t.Parallel()

	type tick struct {
		label string
		minor bool
	}
	// A run of days starting offset days after Monday Sep 7, so that the last
	// Monday falls where each row says.
	for _, tc := range []struct {
		name   string
		locale i18n.Locale
		n      int
		offset int
		today  bool
		want   []tick
	}{
		{"last Monday at 85%, English", i18n.En, 30, 3, true, []tick{{"Sep 14", false}, {"Sep 21", false}, {"Sep 28", false}, {"Oct 5", true}, {"Today", false}}},
		{"last Monday at 85%, Chinese", i18n.ZhHant, 30, 3, true, []tick{{"9/14", false}, {"9/21", false}, {"9/28", false}, {"10/5", true}, {"今天", false}}},
		{"last Monday at 81.7%, English", i18n.En, 30, 4, true, []tick{{"Sep 14", false}, {"Sep 21", false}, {"Sep 28", false}, {"Oct 5", true}, {"Today", false}}},
		{"last Monday at 81.7%, Chinese", i18n.ZhHant, 30, 4, true, []tick{{"9/14", false}, {"9/21", false}, {"9/28", false}, {"10/5", false}, {"今天", false}}},
		{"last Monday at 91.7% is too near for any plot, English", i18n.En, 30, 1, true, []tick{{"Sep 14", false}, {"Sep 21", false}, {"Sep 28", false}, {"Today", false}}},
		{"last Monday at 91.7% is too near for any plot, Chinese", i18n.ZhHant, 30, 1, true, []tick{{"9/14", false}, {"9/21", false}, {"9/28", false}, {"今天", false}}},
		{"a period that ended keeps its last day", i18n.En, 30, 1, false, []tick{{"Sep 14", false}, {"Sep 21", false}, {"Sep 28", false}, {"Oct 5", false}, {"Oct 7", false}}},
		{"14 days ending today", i18n.En, 14, 1, true, []tick{{"Sep 14", false}, {"Today", false}}},
	} {
		cols := Series{Buckets: days(tc.n + tc.offset)[tc.offset:]}.Columns()
		got := make([]tick, 0, len(cols))
		for _, tk := range columnTicks(i18n.WithLocale(t.Context(), tc.locale), cols, false, tc.today, 100/float64(len(cols))) {
			got = append(got, tick{tk.Label, tk.Minor})
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: ticks %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestColumnTicksNeverPrintWithin4pxOfToday(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		ctx := i18n.WithLocale(t.Context(), locale)
		today := textWidth(i18n.T(ctx, i18n.KeyChartToday))
		for offset := range 7 {
			cols := Series{Buckets: days(30 + offset)[offset:]}.Columns()
			for _, tk := range columnTicks(ctx, cols, false, true, 100/float64(len(cols))) {
				if tk.Today {
					continue
				}
				x, err := strconv.ParseFloat(strings.TrimSuffix(tk.X, "%"), 64)
				if err != nil {
					t.Fatalf("tick X %q: %v", tk.X, err)
				}
				// The plot widths: 476, the widest that hides minor ticks, and
				// narrowPlot, the narrowest there is.
				for _, plot := range []float64{widestMinorPlot, narrowPlot} {
					if plot == widestMinorPlot || !tk.Minor {
						gap := plot - today - (x/100*plot + textWidth(tk.Label)/2)
						if gap < 4-0.1 {
							t.Errorf("%s, start +%d: %q (minor %v) is %.1fpx from Today on a %.0fpx plot, want 4 or more", locale, offset, tk.Label, tk.Minor, gap, plot)
						}
					}
				}
			}
		}
	}
}

func TestNameAtKeepsAStripsNameInsideThePlot(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		p          plan
		n          int
		wantText   string
		wantAnchor string
	}{
		{"fits at its start", plan{a: 2, b: 5, label: "Tea week", short: "Tea week"}, 30, "Tea week", "start"},
		{"late in the plot it ends at its strip's end", plan{a: 25, b: 29, label: "Tea week", short: "Tea week"}, 30, "Tea week", "end"},
		{"a running one ends at the edge of today", plan{a: 20, b: 30, runs: true, label: "Autumn picks, until Oct 12", short: "Autumn picks"}, 30, "Autumn picks, until Oct 12", "end"},
		{"too wide for the plot, it drops the day it ends", plan{a: 0, b: 30, runs: true, label: "A very long campaign name, until Oct 12", short: "A very long campaign name"}, 30, "A very long campaign name", "start"},
		{"a running one that fits from its start is written from it", plan{a: 8, b: 30, runs: true, label: "Autumn picks, until Oct 16", short: "Autumn picks"}, 30, "Autumn picks, until Oct 16", "start"},
	} {
		text, _, anchor, left, right := nameAt(tc.p, tc.n)
		if text != tc.wantText || anchor != tc.wantAnchor {
			t.Errorf("%s: nameAt = %q, %s, want %q, %s", tc.name, text, anchor, tc.wantText, tc.wantAnchor)
		}
		if left < 0 || right > narrowPlot {
			t.Errorf("%s: the name takes %.0f to %.0f px, outside the %d px plot", tc.name, left, right, narrowPlot)
		}
	}

	long := plan{a: 0, b: 30, label: strings.Repeat("長", 40), short: strings.Repeat("長", 40)}
	text, _, _, left, right := nameAt(long, 30)
	if !strings.HasSuffix(text, "…") || left < 0 || right > narrowPlot {
		t.Errorf("a name wider than the plot is %q taking %.0f to %.0f px, want it cut with …", text, left, right)
	}
}

// The names fit a plot of narrowPlot at 12px text and no narrower, so the
// stylesheet drops them from a figure narrower than that plot and its value
// axis. The bound is in rem, so that it holds at 200% text too.
func TestColumnsDropStripNamesFromAFigureNarrowerThanTheyAreFittedTo(t *testing.T) {
	t.Parallel()

	sheet, err := os.ReadFile(filepath.Join("..", "..", "..", "assets", "css", "app", "admin.css"))
	if err != nil {
		t.Fatalf("read admin.css: %v", err)
	}
	axis := regexp.MustCompile(`\.goen-chart__frame--columns \{\s*grid-template-columns: ([0-9.]+)rem `).FindSubmatch(sheet)
	if axis == nil {
		t.Fatal("admin.css gives the columns' value axis no width in rem")
	}
	axisWidth, err := strconv.ParseFloat(string(axis[1]), 64)
	if err != nil {
		t.Fatalf("value axis width %q: %v", axis[1], err)
	}
	// textWidth counts in 12px, which is --fs-12, 0.75rem.
	figure := strconv.FormatFloat(narrowPlot*0.75/12+axisWidth, 'f', -1, 64) + "rem"
	drop := regexp.MustCompile(`@container \(width < ` + regexp.QuoteMeta(figure) + `\) \{\s*\.goen-chart__spanlabel \{\s*display: none;`)
	if !drop.Match(sheet) {
		t.Errorf("admin.css does not drop .goen-chart__spanlabel from a figure narrower than %s, a %d px plot at 12px text and a %srem value axis", figure, narrowPlot, axis[1])
	}
}

// lanesOf is the lane each strip is drawn in, counted from the top, or -1 for
// one that has none.
func lanesOf(strips []strip) []int {
	lanes := make([]int, len(strips))
	for k, s := range strips {
		lanes[k] = -1
		if !s.Hidden {
			lanes[k] = int((s.Y - laneBracketAt - laneTop) / laneHeight)
		}
	}
	return lanes
}

func TestColumnsPutCampaignsInLanesByTheDayTheyBegin(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		spans []Span
		want  []int
	}{
		{"none", nil, []int{}},
		{"one", []Span{campaign("Tea week", 3, 5)}, []int{0}},
		{"overlapping ones never share", []Span{campaign("First campaign", 3, 12), campaign("Second campaign", 5, 15)}, []int{0, 1}},
		{"one that begins on the same day is below", []Span{campaign("First campaign", 3, 12), campaign("Second campaign", 3, 4)}, []int{0, 1}},
		{"the order given does not matter", []Span{campaign("Second campaign", 5, 15), campaign("First campaign", 3, 12)}, []int{1, 0}},
		{"one that begins after another ends takes its lane", []Span{campaign("Tea week", 0, 2), campaign("Autumn picks", 12, 20)}, []int{0, 0}},
		{"a name that runs into the next campaign keeps it below", []Span{campaign("A long campaign name", 0, 1), campaign("Next", 4, 8)}, []int{0, 1}},
		{"three at once", []Span{campaign("One", 1, 20), campaign("Two", 2, 20), campaign("Three", 3, 20)}, []int{0, 1, 2}},
		{"a fourth at once has none", []Span{campaign("One", 1, 20), campaign("Two", 2, 20), campaign("Three", 3, 20), campaign("Four", 4, 20)}, []int{0, 1, 2, -1}},
		{"a day alone", []Span{campaign("One day", 9, 9)}, []int{0}},
		{"began before the chart", []Span{campaign("Long sale", -20, 4), campaign("Tea week", 2, 6)}, []int{0, 1}},
		{"ends after the chart", []Span{campaign("Long sale", 25, 60), campaign("Tea week", 20, 26)}, []int{1, 0}},
		{"outside the chart", []Span{campaign("Last month", -40, -20), campaign("Next month", 40, 50)}, []int{}},
		{"back to back, names crowd the lanes before the days do", []Span{campaign("Tea week", 0, 1), campaign("Book week", 2, 3), campaign("Gift week", 4, 5), campaign("Home week", 6, 7)}, []int{0, 1, 2, -1}},
	} {
		p := columnsProps(valued(30, map[int]int64{3: 2, 4: 5, 20: 1, 29: 3}))
		p.Spans = tc.spans
		c := newColumns(t.Context(), p)
		if got := lanesOf(c.Strips); !slices.Equal(got, tc.want) {
			t.Errorf("%s: lanes = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestColumnsOpenTheEndOfABracketThatIsOffTheChart(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		span Span
		ends int
	}{
		{"within the chart", campaign("Tea week", 3, 5), 2},
		{"began before the chart", campaign("Long sale", -20, 4), 1},
		{"began before and goes on", campaign("Long sale", -20, 40), 0},
	} {
		p := columnsProps(valued(30, map[int]int64{3: 2, 4: 5, 20: 1, 29: 3}))
		p.Spans = []Span{tc.span}
		if n := strings.Count(renderColumns(t, i18n.En, p), `class="goen-chart__strip-end"`); n != tc.ends {
			t.Errorf("%s: %d bracket ends, want %d", tc.name, n, tc.ends)
		}
	}
}

func TestColumnsMuteTheNameOfTheDaysBefore(t *testing.T) {
	t.Parallel()

	got := renderColumns(t, i18n.En, compared(true))
	if !strings.Contains(got, `class="goen-chart__spanlabel goen-chart__spanlabel--window"`) {
		t.Error("the name of the days before is drawn like a campaign's")
	}
	if n := strings.Count(got, `class="goen-chart__spanlabel"`); n != 1 {
		t.Errorf("%d campaign names in the ink colour, want the campaign's alone", n)
	}
}

func TestColumnsBringTheLanesDownToTheColumnsTheyMark(t *testing.T) {
	t.Parallel()

	// The axis of 20 leaves a bar of 20 no room under its top, so a value over it
	// takes the whole of valueRoom; a lower one has room already.
	for _, tc := range []struct {
		highest int64
		want    float64
	}{
		{20, valueRoom},
		{19, 14.5},
		{16, minValueRoom},
	} {
		at := map[int]int64{21: tc.highest, 29: 3}
		for i := range 7 {
			at[i*3] = int64(i + 1)
		}
		p := columnsProps(valued(30, at))
		p.Spans = []Span{campaign("Tea week", 3, 5)}
		c := newColumns(t.Context(), p)
		if got, want := c.Baseline-columnsPlot, tc.want+laneTop+laneHeight; got != want {
			t.Errorf("highest %d: the columns begin at y %.1f, want %.1f", tc.highest, got, want)
		}
	}
}

func TestColumnsKeepLanesApartFromEachOtherAndFromTheValues(t *testing.T) {
	t.Parallel()

	p := columnsProps(valued(30, map[int]int64{3: 2, 4: 5, 20: 20, 29: 3}))
	p.Spans = []Span{campaign("First campaign", 3, 12), campaign("Second campaign", 5, 15), campaign("Third campaign", 6, 40)}
	c := newColumns(t.Context(), p)
	if len(c.Strips) != 3 {
		t.Fatalf("%d strips, want 3", len(c.Strips))
	}
	// A name is 12px text: about 10px above its baseline and 3 below. A bracket is
	// its line and the ends that hang 5px under it, 1.5px thick.
	const above, below, thick = 10, 3, 0.75
	for _, a := range c.Strips {
		for _, b := range c.Strips {
			if a.Y == b.Y {
				continue
			}
			if a.Y-thick < b.NameY+below && b.NameY-above < a.Y+5+thick {
				t.Errorf("the bracket at y %.1f to %.1f runs through the name at y %.1f to %.1f", a.Y-thick, a.Y+5+thick, b.NameY-above, b.NameY+below)
			}
		}
	}
	for _, v := range c.Values {
		for _, s := range c.Strips {
			if v.Y-above < s.Y+5+thick {
				t.Errorf("the value %s at y %.1f reaches up into the bracket ending at y %.1f", v.Text, v.Y-above, s.Y+5+thick)
			}
		}
	}
	if want := float64(valueRoom + laneTop + laneHeight*3); c.Baseline-columnsPlot != want {
		t.Errorf("the columns begin at y %.0f, want %.0f: the lanes and the room for a value", c.Baseline-columnsPlot, want)
	}
	if plain := newColumns(t.Context(), columnsProps(valued(30, map[int]int64{3: 2, 4: 5, 20: 1, 29: 3}))); plain.Baseline-columnsPlot != valueRoom {
		t.Errorf("with no campaign the columns begin at y %.0f, want %d", plain.Baseline-columnsPlot, valueRoom)
	}
}

func TestColumnsOfSevenDaysKeepBothSentencesUnderTheChart(t *testing.T) {
	t.Parallel()

	p := columnsProps(valued(90, map[int]int64{0: 1, 10: 2, 50: 3, 60: 1, 89: 4}))
	p.Spans = []Span{campaign("One", 10, 80), campaign("Two", 11, 80), campaign("Three", 12, 80), campaign("Four", 13, 80)}
	want := `<p class="goen-chart__note">The earliest stretch has 6 days. Counted up to 15:20. 1 more campaign is shaded without a name; the table names it.</p>`
	if got := renderColumns(t, i18n.En, p); !strings.Contains(got, want) {
		t.Errorf("the note under a chart of runs of seven days is not %s\n%s", want, got)
	}

	p.Note = ""
	if got, want := newColumns(i18n.WithLocale(t.Context(), i18n.En), p).Note, "The earliest stretch has 6 days. 1 more campaign is shaded without a name; the table names it."; got != want {
		t.Errorf("a chart with no note of its own says %q, want %q", got, want)
	}
}

func TestColumnsNameTheCampaignsThatGotNoLane(t *testing.T) {
	t.Parallel()

	p := columnsProps(valued(30, map[int]int64{3: 2, 4: 5, 20: 1, 29: 3}))
	p.Spans = []Span{
		campaign("One", 1, 20), campaign("Two", 2, 20), campaign("Three", 3, 20),
		campaign("Four", 4, 20), campaign("Five", 5, 20),
	}
	for _, tc := range []struct {
		locale    i18n.Locale
		separator string
		want      string
	}{
		{i18n.En, ", ", "Counted up to 15:20. 2 more campaigns are shaded without names; the table names them."},
		{i18n.ZhHant, "、", "另有 2 檔活動在圖上只有底色，名稱列在表格裡。"},
	} {
		got := renderColumns(t, tc.locale, p)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: the note does not say %q\n%s", tc.locale, tc.want, got)
		}
		if strings.Contains(got, "at the same time") || strings.Contains(got, "同時進行") {
			t.Errorf("%s: the note says the campaigns ran at once, but a fourth can be left out for want of room for its name", tc.locale)
		}
		if n := strings.Count(got, `class="goen-chart__strip"`); n != 3 {
			t.Errorf("%s: %d brackets, want 3", tc.locale, n)
		}
		if n := strings.Count(got, `class="goen-chart__span"`); n != 5 {
			t.Errorf("%s: %d grounds, want all 5: a campaign without a bracket still shades its days", tc.locale, n)
		}
		for _, name := range []string{">Four<", ">Five<"} {
			if strings.Contains(got, name) {
				t.Errorf("%s: %s is drawn over the chart", tc.locale, name)
			}
		}
		if !strings.Contains(got, `<td class="goen-chart__spans">One`+tc.separator+`Two`+tc.separator+`Three`+tc.separator+`Four`+tc.separator+`Five</td>`) {
			t.Errorf("%s: the table does not name the campaigns left out", tc.locale)
		}
	}

	one := columnsProps(valued(30, map[int]int64{3: 2, 4: 5, 20: 1, 29: 3}))
	one.Spans = []Span{campaign("One", 1, 20), campaign("Two", 2, 20), campaign("Three", 3, 20), campaign("Four", 4, 20)}
	if got := renderColumns(t, i18n.En, one); !strings.Contains(got, "1 more campaign is shaded without a name; the table names it.") {
		t.Error("one campaign left out is not said in the singular")
	}
	if got := renderColumns(t, i18n.En, columnsProps(valued(30, map[int]int64{3: 2, 4: 5, 20: 1, 29: 3}))); strings.Contains(got, "shaded without") {
		t.Error("a chart that left nothing out says it did")
	}
}

// compared is 14 days from 2026-09-07: the first seven are the days before a
// campaign that runs from the 14th.
func compared(partial bool) ColumnsProps {
	p := columnsProps(valued(14, map[int]int64{0: 1, 3: 2, 6: 4, 7: 1, 9: 5, 10: 6, 13: 3}))
	p.Series.Partial = partial
	p.Spans = []Span{{
		From: time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC),
		Label: "Autumn picks",
	}}
	p.Previous, p.PreviousLabel = 7, "Before"
	return p
}

func TestColumnsDrawTheDaysBeforeInTheirOwnColourUnderALine(t *testing.T) {
	t.Parallel()

	got := renderColumns(t, i18n.En, compared(true))
	if n := strings.Count(got, "goen-chart__hue--previous"); n != 3 {
		t.Errorf("%d bars in the previous colour, want the 3 days before that have sales", n)
	}
	if n := strings.Count(got, `class="goen-chart__hue"`) + strings.Count(got, `class="goen-chart__hue goen-chart__open"`); n != 4 {
		t.Errorf("%d bars in the data colour, want the 4 campaign days with sales", n)
	}
	if n := strings.Count(got, `class="goen-chart__window"`); n != 1 {
		t.Errorf("%d lines under the days before, want 1", n)
	}
	if c := newColumns(i18n.WithLocale(t.Context(), i18n.En), compared(true)); len(c.Strips) != 2 || c.Strips[0].Y != c.Strips[1].Y {
		t.Errorf("strips = %+v, want the line under the days before and the campaign that starts where it stops in one lane", c.Strips)
	}
	if n := strings.Count(got, `class="goen-chart__strip"`); n != 1 {
		t.Errorf("%d campaign brackets, want 1: the days before are not a stored period", n)
	}
	if n := strings.Count(got, `class="goen-chart__strip-end"`); n != 1 {
		t.Errorf("%d bracket ends, want the campaign's start: the days before have none, and the campaign goes on", n)
	}
	if !strings.Contains(got, ">Before</text>") || !strings.Contains(got, ">Autumn picks, until Sep 30</text>") {
		t.Error("the drawing does not name both parts")
	}
}

func TestColumnsTableSaysWhichDaysAreBeforeAndWhichAreTheCampaign(t *testing.T) {
	t.Parallel()

	got := renderColumns(t, i18n.En, compared(true))
	for _, row := range []string{
		`<th scope="row">Sep 7</th><td>1</td><td class="goen-chart__spans">Before</td>`,
		`<th scope="row">Sep 13</th><td>4</td><td class="goen-chart__spans">Before</td>`,
		`<th scope="row">Sep 20 (up to 15:20)</th>`,
	} {
		if !strings.Contains(strings.ReplaceAll(got, "\n", ""), row) {
			t.Errorf("table lacks %q", row)
		}
	}
	if !strings.Contains(got, `<td class="goen-chart__spans">Autumn picks, until Sep 30</td>`) {
		t.Error("a campaign day's row does not name the campaign")
	}
}

func TestColumnsWithoutPreviousDrawNoWindow(t *testing.T) {
	t.Parallel()

	p := compared(true)
	p.Previous, p.PreviousLabel = 0, ""
	got := renderColumns(t, i18n.En, p)
	if strings.Contains(got, "goen-chart__window") || strings.Contains(got, "goen-chart__hue--previous") || strings.Contains(got, ">Before<") {
		t.Error("a series with no Previous draws a stretch before")
	}
}

func TestColumnsEndingBeforeTodayAreNotMarkedAsToday(t *testing.T) {
	t.Parallel()

	got := renderColumns(t, i18n.En, compared(false))
	for _, c := range []string{"goen-chart__todaymark", "goen-chart__today\"", ">Today<"} {
		if strings.Contains(got, c) {
			t.Errorf("a series whose last day is over contains %q", c)
		}
	}
	if !strings.Contains(renderColumns(t, i18n.En, compared(true)), ">Today<") {
		t.Error("a series whose last day is going has no Today tick")
	}
}

func TestColumnsHitsNameTheirTableRows(t *testing.T) {
	t.Parallel()

	got := renderColumns(t, i18n.En, columnsProps(valued(30, map[int]int64{3: 2, 9: 5, 20: 1, 29: 4})))
	if n := strings.Count(got, `class="goen-chart__hit"`); n != 30 {
		t.Errorf("columns of 30 days: %d hits, want 30", n)
	}
	if n := strings.Count(got, "<tr>") - 1; n != 30 {
		t.Errorf("columns of 30 days: %d table rows, want 30", n)
	}
	for _, want := range []string{`<p class="goen-chart__readout"></p>`, `data-row="0"`, `data-row="29"`, `data-readout="series">Paid orders<`} {
		if !strings.Contains(got, want) {
			t.Errorf("columns of 30 days: markup lacks %s", want)
		}
	}
	if strings.Contains(got, "data-x=") || strings.Contains(got, "tabindex") {
		t.Error("columns: a hit carries a crosshair position or the markup has a tab stop")
	}
}
