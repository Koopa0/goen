package chart

import (
	"bytes"
	"slices"
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
		Current:      Series{Label: "This period", Buckets: filled(10, 100_000), Partial: true},
		Previous:     Series{Label: "Previous", Buckets: filled(10, 50_000), Partial: true},
		Measure:      MeasureMoney,
		Caption:      "Revenue over 10 days: NT$10,000.",
		Note:         "Counted up to 15:20.",
		DayHeading:   "Date",
		TotalLabel:   "Total",
		PartialLabel: "up to 15:20",
	}
}

func renderRunningTotal(t *testing.T, p RunningTotalProps) string {
	t.Helper()
	var b bytes.Buffer
	if err := RunningTotal(p).Render(i18n.WithLocale(t.Context(), i18n.En), &b); err != nil {
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
		got := renderRunningTotal(t, p)
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

	got := renderRunningTotal(t, fullProps())
	for _, want := range []string{
		`<figcaption class="goen-chart__caption">Revenue over 10 days: NT$10,000.</figcaption>`,
		`class="goen-chart__endvalue"`,
		">NT$10,000<", // 10 days of NT$1,000
		">NT$5,000<",
		"<summary>Show as a table</summary>",
		`<tfoot><tr><th scope="row">Total</th><td>NT$10,000</td><td>NT$5,000</td></tr></tfoot>`,
		`<p class="goen-chart__note">Counted up to 15:20.</p>`,
		">Today<",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RunningTotal does not contain %s", want)
		}
	}
	if strings.Contains(got, "style=") {
		t.Error("RunningTotal sets a style attribute, which the content security policy refuses")
	}
	for _, svg := range strings.Split(got, "<svg")[1:2] {
		if !strings.Contains(svg, `aria-hidden="true"`) {
			t.Errorf("the drawing is not hidden from assistive technology: <svg%s", svg[:80])
		}
	}
}

func TestRunningTotalDrawsAnUnfinishedDayOpen(t *testing.T) {
	t.Parallel()

	partial := renderRunningTotal(t, fullProps())
	if got := strings.Count(partial, "goen-chart__end--open"); got != 2 {
		t.Errorf("both periods end mid-day, but %d ends are open, want 2", got)
	}
	if !strings.Contains(partial, "<th scope=\"row\">Sep 16 (up to 15:20)</th>") {
		t.Errorf("the table does not say that its last day is counted up to 15:20:\n%s", partial)
	}

	whole := fullProps()
	whole.Current.Partial, whole.Previous.Partial = false, false
	got := renderRunningTotal(t, whole)
	if strings.Contains(got, "goen-chart__end--open") || strings.Contains(got, "up to 15:20)") {
		t.Errorf("a whole last day is drawn as unfinished:\n%s", got)
	}
}

func TestRunningTotalAxisIsOneUnitForEveryLabel(t *testing.T) {
	t.Parallel()

	p := fullProps()
	p.Current.Buckets = filled(10, 20_000_000) // NT$200,000 a day, NT$2,000,000 in all
	p.Previous.Buckets = filled(10, 10_000_000)
	zh := newRunningTotal(i18n.WithLocale(t.Context(), i18n.ZhHant), p)
	en := newRunningTotal(i18n.WithLocale(t.Context(), i18n.En), p)

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
