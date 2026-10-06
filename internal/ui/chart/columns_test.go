package chart

import (
	"bytes"
	"slices"
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
		`<th scope="col" class="goen-chart__spans">Campaign</th>`,
		`<th scope="row">Sep 10</th><td>2</td><td class="goen-chart__spans">Tea week</td>`,
		`<th scope="row">Sep 12</th><td>0</td><td class="goen-chart__spans">Tea week</td>`,
		`<th scope="row">Sep 13</th><td>0</td><td class="goen-chart__spans"></td>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Columns does not contain %s\n%s", want, got)
		}
	}
	if n := strings.Count(got, `class="goen-chart__gap`); n != 2 {
		t.Errorf("%d gaps in a strip of 3 days, want 2 between them", n)
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

	if n := strings.Count(got, `class="goen-chart__todaymark"`); n != 2 {
		t.Errorf("%d today marks, want one under the column and one across the strip", n)
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
	if strings.Contains(got, `class="goen-chart__gap`) {
		t.Error("a strip over columns of seven days is cut into days")
	}
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
		ticks := columnTicks(i18n.WithLocale(t.Context(), i18n.En), Series{Buckets: days(n)}.Columns(), false, 100/float64(n))
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

func TestNameAtKeepsAStripsNameInsideThePlot(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		p          plan
		n          int
		wantText   string
		wantAnchor string
	}{
		{"fits at its start", plan{a: 2, b: 5, end: 5, label: "Tea week", short: "Tea week"}, 30, "Tea week", "start"},
		{"late in the plot it ends at its strip's end", plan{a: 25, b: 29, end: 29, label: "Tea week", short: "Tea week"}, 30, "Tea week", "end"},
		{"a running one ends at today", plan{a: 20, b: 30, end: 29.5, runs: true, label: "Autumn picks, until Oct 12", short: "Autumn picks"}, 30, "Autumn picks, until Oct 12", "end"},
		{"too wide for the plot, it drops the day it ends", plan{a: 0, b: 30, end: 29.5, runs: true, label: "A very long campaign name, until Oct 12", short: "A very long campaign name"}, 30, "A very long campaign name", "end"},
	} {
		text, _, anchor, left, right := nameAt(tc.p, tc.n)
		if text != tc.wantText || anchor != tc.wantAnchor {
			t.Errorf("%s: nameAt = %q, %s, want %q, %s", tc.name, text, anchor, tc.wantText, tc.wantAnchor)
		}
		if left < 0 || right > narrowPlot {
			t.Errorf("%s: the name takes %.0f to %.0f px, outside the %d px plot", tc.name, left, right, narrowPlot)
		}
	}

	long := plan{a: 0, b: 30, end: 30, label: strings.Repeat("長", 40), short: strings.Repeat("長", 40)}
	text, _, _, left, right := nameAt(long, 30)
	if !strings.HasSuffix(text, "…") || left < 0 || right > narrowPlot {
		t.Errorf("a name wider than the plot is %q taking %.0f to %.0f px, want it cut with …", text, left, right)
	}
}

func TestColumnsStripsThatMeetStackInRows(t *testing.T) {
	t.Parallel()

	p := columnsProps(valued(30, map[int]int64{3: 2, 4: 5, 20: 1, 29: 3}))
	p.Spans = []Span{campaign("First campaign", 3, 12), campaign("Second campaign", 5, 15)}
	c := newColumns(t.Context(), p)
	if len(c.Strips) != 2 || c.Strips[0].Y == c.Strips[1].Y {
		t.Errorf("strips = %+v, want two on different rows, their names would run together", c.Strips)
	}
}
