package chart

import (
	"bytes"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
)

// days is n consecutive shop days from Monday 2026-09-07, the first valued v.
func days(n int, values ...int64) []Bucket {
	buckets := make([]Bucket, n)
	for i := range buckets {
		buckets[i].Day = time.Date(2026, 9, 7+i, 0, 0, 0, 0, time.UTC)
		if i < len(values) {
			buckets[i].Value = values[i]
		}
	}
	return buckets
}

func filled(n int, each int64) []Bucket {
	values := slices.Repeat([]int64{each}, n)
	return days(n, values...)
}

func TestDensityCountsTheBucketsThatAreNotZero(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		s    Series
		want Density
	}{
		{"no buckets", Series{}, DensityNone},
		{"only zeros", Series{Buckets: days(30)}, DensityNone},
		{"one", Series{Buckets: days(30, 5)}, DensityFew},
		{"two", Series{Buckets: days(30, 5, 0, 7)}, DensityFew},
		{"three", Series{Buckets: days(30, 1, 1, 1)}, DensitySparse},
		{"six of thirty", Series{Buckets: append(filled(6, 1), days(24)...)}, DensitySparse},
		{"seven", Series{Buckets: filled(7, 1)}, DensityFull},
	} {
		if got := tc.s.Density(); got != tc.want {
			t.Errorf("%s: Density() = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestAxisStepKeepsToFiveLines(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		top  int64
		m    Measure
		want int64
	}{
		{"count of 5", 5, MeasureCount, 1},
		{"count of 6", 6, MeasureCount, 2},
		{"count of 11", 11, MeasureCount, 5},
		{"a count never steps by 2.5, nor by 25", 120, MeasureCount, 50},
		{"count of 26", 26, MeasureCount, 10},
		{"money of 12,000 dollars", 1_200_000, MeasureMoney, 250_000},
		{"money steps by 2.5 where 2 is too short", 1_250_000, MeasureMoney, 250_000},
		{"money of 10,000 dollars", 1_000_000, MeasureMoney, 200_000},
		{"money of 212,292 dollars", 21_229_200, MeasureMoney, 5_000_000},
		{"money of 5 dollars", 500, MeasureMoney, 100},
		{"money never steps by NT$2.5", 1_200, MeasureMoney, 500},
		{"nothing to draw", 0, MeasureMoney, 100},
	} {
		got := axisStep(tc.top, tc.m)
		if got != tc.want {
			t.Errorf("%s: axisStep(%d, %d) = %d, want %d", tc.name, tc.top, tc.m, got, tc.want)
		}
		if lines := gridLines(tc.top, got); lines > maxGridLines {
			t.Errorf("%s: axisStep(%d) draws %d lines, want at most %d", tc.name, tc.top, lines, maxGridLines)
		}
	}
}

func TestTickDaysLabelTheWeekMondaysOrMonthStarts(t *testing.T) {
	t.Parallel()

	starting := func(n int, year int, month time.Month, day int) []time.Time {
		out := make([]time.Time, n)
		for i := range out {
			out[i] = time.Date(year, month, day+i, 0, 0, 0, 0, time.UTC)
		}
		return out
	}
	for _, tc := range []struct {
		name string
		days []time.Time
		want []int
	}{
		{"a week labels each day between the ends", starting(7, 2026, 9, 7), []int{1, 2, 3, 4, 5}},
		{"thirty days label the Mondays", starting(30, 2026, 9, 7), []int{7, 14, 21}},
		{"ninety days label each month's first", starting(90, 2026, 7, 8), []int{24, 55}},
	} {
		if got := tickDays(tc.days); !slices.Equal(got, tc.want) {
			t.Errorf("%s: tickDays = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCumulativeAddsEachDay(t *testing.T) {
	t.Parallel()

	got := cumulative(Series{Buckets: days(5, 3, 0, 4, 0, 5)})
	if want := []int64{3, 3, 7, 7, 12}; !slices.Equal(got, want) {
		t.Errorf("cumulative = %v, want %v", got, want)
	}
}

func TestEndLabelsAreKeptApart(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		a, b         float64
		wantA, wantB float64
	}{
		{"far apart stay", 60, 120, 60, 120},
		{"close ones part around the middle", 80, 90, 70, 100},
		{"the order is kept", 90, 80, 100, 70},
		{"pushed down from the top", 20, 20, 20, 50},
		{"pushed up from the bottom", 144, 144, 114, 144},
	} {
		gotA, gotB := apart(tc.a, tc.b, 20, 144)
		if gotA != tc.wantA || gotB != tc.wantB {
			t.Errorf("%s: apart(%v, %v, 20, 144) = %v, %v, want %v, %v", tc.name, tc.a, tc.b, gotA, gotB, tc.wantA, tc.wantB)
		}
	}
}

func fullProps() RunningTotalProps {
	return RunningTotalProps{
		Current:            Series{Label: "This period", Buckets: filled(10, 100_000), Partial: true},
		Previous:           Series{Label: "Previous", Buckets: filled(10, 50_000), Partial: true},
		Measure:            MeasureMoney,
		Caption:            "Revenue over 10 days: NT$10,000.",
		Note:               "Counted up to 15:20.",
		DayHeading:         "Date",
		PreviousDayHeading: "Previous date",
		TotalLabel:         "Total",
		PartialLabel:       "up to 15:20",
	}
}

func renderRunningTotal(t *testing.T, p *RunningTotalProps) string {
	t.Helper()
	var b bytes.Buffer
	if err := RunningTotal(*p).Render(i18n.WithLocale(t.Context(), i18n.En), &b); err != nil {
		t.Fatalf("RunningTotal.Render: %v", err)
	}
	return b.String()
}

func TestRunningTotalDrawsOnlyFromSevenDaysWithValues(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		current []Bucket
		want    bool
	}{
		{"six days of thirty", append(filled(6, 100_000), days(24)...), false},
		{"seven days of thirty", append(filled(7, 100_000), days(23)...), true},
	} {
		p := fullProps()
		p.Current.Buckets = tc.current
		got := renderRunningTotal(t, &p)
		if drawn := strings.Contains(got, "<svg"); drawn != tc.want {
			t.Errorf("%s: RunningTotal draws = %v, want %v\n%s", tc.name, drawn, tc.want, got)
		}
		if tc.want == false && got != "" {
			t.Errorf("%s: RunningTotal = %q, want nothing, not even a caption", tc.name, got)
		}
	}
}

func TestRunningTotalEndsEachLineInItsTotalAndTheTableEndsInBoth(t *testing.T) {
	t.Parallel()

	got := renderRunningTotal(t, new(fullProps()))
	for _, want := range []string{
		`<figcaption class="goen-chart__caption">Revenue over 10 days: NT$10,000.</figcaption>`,
		`class="goen-chart__endvalue"`,
		">NT$10,000<", // 10 days of NT$1,000
		">NT$5,000<",
		"<summary>Show as a table</summary>",
		`<tfoot><tr><th scope="row">Total</th><td>NT$10,000</td><td></td><td>NT$5,000</td></tr></tfoot>`,
		`<p class="goen-chart__note">Counted up to 15:20.</p>`,
		">Today<",
		`class="goen-chart__endkey goen-chart__endkey--current"`,
		`class="goen-chart__endkey goen-chart__endkey--previous"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RunningTotal does not contain %s", want)
		}
	}
	if strings.Contains(got, "style=") {
		t.Error("RunningTotal sets a style attribute, which the content security policy refuses")
	}
	if outer, hidden := strings.Count(got, `<svg class="goen-chart__`), strings.Count(got, `aria-hidden="true" focusable="false"`); outer != hidden || outer != 3 {
		t.Errorf("the drawing has %d outer SVGs and %d of them hidden from assistive technology, want 3 and 3", outer, hidden)
	}
}

// The table carries what the drawing says, including which day of the previous
// period each of its figures is for, and says it once.
func TestRunningTotalTableNamesThePreviousPeriodsDays(t *testing.T) {
	t.Parallel()

	p := fullProps()
	p.Previous.Buckets = filled(10, 50_000)
	for i := range p.Previous.Buckets {
		p.Previous.Buckets[i].Day = time.Date(2026, 8, 1+i, 0, 0, 0, 0, time.UTC)
	}
	got := renderRunningTotal(t, &p)
	for _, want := range []string{
		`<th scope="col">Previous date</th>`,
		`<th scope="row">Sep 7</th><td>NT$1,000</td><td>Aug 1</td><td>NT$500</td>`,
		`<th scope="row">Sep 16 (up to 15:20)</th><td>NT$10,000</td><td>Aug 10</td><td>NT$5,000</td>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the table does not contain %s", want)
		}
	}
	if strings.Contains(got, "<caption>") {
		t.Error("the table repeats the figure's caption, which a screen reader would read twice")
	}
}

func TestRunningTotalDrawsAnUnfinishedDayOpen(t *testing.T) {
	t.Parallel()

	partial := renderRunningTotal(t, new(fullProps()))
	if got := strings.Count(partial, "goen-chart__end--open"); got != 2 {
		t.Errorf("both periods end mid-day, but %d ends are open, want 2", got)
	}
	if !strings.Contains(partial, "<th scope=\"row\">Sep 16 (up to 15:20)</th>") {
		t.Errorf("the table does not say that its last day is counted up to 15:20:\n%s", partial)
	}

	whole := fullProps()
	whole.Current.Partial, whole.Previous.Partial = false, false
	got := renderRunningTotal(t, &whole)
	if strings.Contains(got, "goen-chart__end--open") || strings.Contains(got, "up to 15:20)") {
		t.Errorf("a whole last day is drawn as unfinished:\n%s", got)
	}
}

func TestRunningTotalAxisIsOneUnitForEveryLabel(t *testing.T) {
	t.Parallel()

	p := fullProps()
	p.Current.Buckets = filled(10, 20_000_000) // NT$200,000 a day, NT$2,000,000 in all
	p.Previous.Buckets = filled(10, 10_000_000)
	zh := newRunningTotal(i18n.WithLocale(t.Context(), i18n.ZhHant), &p)
	en := newRunningTotal(i18n.WithLocale(t.Context(), i18n.En), &p)

	labels := func(r runningTotal) []string {
		out := make([]string, 0, len(r.Grid))
		for _, g := range r.Grid {
			out = append(out, g.Label)
		}
		return out
	}
	if got, want := labels(zh), []string{"0", "50萬", "100萬", "150萬", "200萬"}; !slices.Equal(got, want) {
		t.Errorf("zh axis = %v, want %v", got, want)
	}
	if got, want := labels(en), []string{"0", "0.5M", "1M", "1.5M", "2M"}; !slices.Equal(got, want) {
		t.Errorf("en axis = %v, want %v", got, want)
	}
}

// Every label on the axis, read back in the axis' unit, is the value of its
// line: an axis that prints 0.2萬 for NT$2,500 is a wrong amount.
func TestAxisLabelsAreTheValuesOfTheirLines(t *testing.T) {
	t.Parallel()

	tops := []int64{300, 1_250, 2_500, 99_900, 1_200_000, 1_250_000, 3_300_000, 21_229_200, 1_200_000_000, 12_000_000_000, 120_000_000_000}
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, top := range tops {
			p := fullProps()
			p.Current.Buckets = filled(10, top/10)
			p.Previous.Buckets = days(10)
			r := newRunningTotal(ctx, &p)

			step := axisStep(top, MeasureMoney)
			divisor, suffix := i18n.AxisUnit(ctx, step*gridLines(top, step)/100)
			for k, g := range r.Grid {
				got, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSuffix(g.Label, suffix), ",", ""), 64)
				if err != nil {
					t.Fatalf("%s, top %d: label %q is not a number: %v", locale, top, g.Label, err)
				}
				if want := int64(k) * step; int64(math.Round(got*float64(divisor)*100)) != want {
					t.Errorf("%s, top %d: line %d is %d cents, its label %q reads %v cents",
						locale, top, k, want, g.Label, got*float64(divisor)*100)
				}
			}
		}
	}
}

func TestRunningTotalHitsEndEachDayWhereItsTotalStands(t *testing.T) {
	t.Parallel()

	p := fullProps()
	got := renderRunningTotal(t, &p)
	n := len(p.Current.Buckets)
	if c := strings.Count(got, `class="goen-chart__hit"`); c != n {
		t.Errorf("running total of %d days: %d hits, want %d", n, c, n)
	}
	last := `data-row="` + strconv.Itoa(n-1) + `" data-x="100.00%"`
	if !strings.Contains(got, last) {
		t.Errorf("running total: the last hit does not end at 100%%, want %s", last)
	}
	if !strings.Contains(got, `<p class="goen-chart__readout"></p>`) {
		t.Error("running total: no empty readout for goen.js to fill")
	}
	if c := strings.Count(got, `data-readout="series"`); c != 2 {
		t.Errorf("running total: %d series columns, want 2", c)
	}
	if strings.Contains(got, "tabindex") {
		t.Error("running total: the markup has a tab stop")
	}
}
