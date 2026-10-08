package chart

import (
	"bytes"
	"strings"
	"testing"
)

func renderBar(t *testing.T, p BarProps) string {
	t.Helper()
	var b bytes.Buffer
	if err := Bar(p).Render(t.Context(), &b); err != nil {
		t.Fatalf("Bar(%+v).Render: %v", p, err)
	}
	return b.String()
}

// svgOf is the drawing alone, without the count beside it.
func svgOf(t *testing.T, html string) string {
	t.Helper()
	start, end := strings.Index(html, "<svg"), strings.Index(html, "</svg>")
	if start < 0 || end < start {
		t.Fatalf("no <svg> in %s", html)
	}
	return html[start:end]
}

func TestBarWidthIsScaledToTheMax(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		p    BarProps
		want string
	}{
		{"the longest fills the track", BarProps{Value: 40, Max: 40}, "100.00%"},
		{"half of the max", BarProps{Value: 20, Max: 40}, "50.00%"},
		{"a tie is as long as the max", BarProps{Value: 7, Max: 7}, "100.00%"},
		{"tiny stays visible", BarProps{Value: 1, Max: 100000}, "0.60%"},
		{"above the max is clamped", BarProps{Value: 50, Max: 40}, "100.00%"},
		{"zero has no length", BarProps{Value: 0, Max: 40}, "0.00%"},
		{"no max has no length", BarProps{Value: 5, Max: 0}, "0.00%"},
	} {
		if got := tc.p.width(); got != tc.want {
			t.Errorf("%s: BarProps%+v.width() = %q, want %q", tc.name, tc.p, got, tc.want)
		}
	}
}

func TestBarFillsATrackWithItsCountAsText(t *testing.T) {
	t.Parallel()

	got := renderBar(t, BarProps{Value: 20, Max: 40, Label: "20"})
	svg := svgOf(t, got)
	for _, want := range []string{
		`class="goen-chartbar__track"`,
		`class="goen-chartbar__fill"`,
		`width="50.00%"`,
		`class="goen-chartbar__track" x="0" y="0" width="100%" height="8"`,
		`class="goen-chartbar__fill" x="0" y="0" width="50.00%" height="8"`,
		`aria-hidden="true"`,
		`focusable="false"`,
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("Bar(20 of 40) draws %s, want it to contain %s", svg, want)
		}
	}
	if strings.Contains(svg, "<text") {
		t.Errorf("Bar(20 of 40) draws %s, want the count outside the SVG", svg)
	}
	if !strings.Contains(got, `<span class="goen-chartbar__label">20</span>`) {
		t.Errorf("Bar(20 of 40) = %s, want the count 20 as text beside the bar", got)
	}
	if !strings.HasPrefix(got, `<div class="goen-chartbar" aria-hidden="true">`) {
		t.Errorf("Bar(20 of 40) = %s, want the bar and its count hidden from assistive technology, which reads the row's own text", got)
	}
	if strings.Contains(got, "style=") {
		t.Errorf("Bar(20 of 40) = %s, want no style attribute", got)
	}
}

func TestBarOfZeroDrawsNoFillButKeepsItsLabel(t *testing.T) {
	t.Parallel()

	got := renderBar(t, BarProps{Value: 0, Max: 40, Label: "0"})
	if strings.Contains(got, "goen-chartbar__fill") {
		t.Errorf("Bar(0 of 40) = %s, want no fill", got)
	}
	if !strings.Contains(got, `<span class="goen-chartbar__label">0</span>`) {
		t.Errorf("Bar(0 of 40) = %s, want its label", got)
	}
}

func TestCountColumnWidensWithTheScale(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		max  int64
		want bool
	}{
		{999, false},
		{1000, true},
		{25000, true},
	} {
		got := strings.Contains(renderBar(t, BarProps{Value: 1, Max: tc.max, Label: "1"}), "goen-chartbar--wide")
		if got != tc.want {
			t.Errorf("Bar(1 of %d) widens its count column = %v, want %v", tc.max, got, tc.want)
		}
	}
}

func TestMeterWidthIsTheShareOfTheLimit(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		p    MeterProps
		want string
	}{
		{"unused", MeterProps{Value: 0, Limit: 10}, "0.00%"},
		{"a quarter", MeterProps{Value: 5, Limit: 20}, "25.00%"},
		{"used up", MeterProps{Value: 10, Limit: 10}, "100.00%"},
		{"above the limit is clamped", MeterProps{Value: 12, Limit: 10}, "100.00%"},
		{"no limit has no length", MeterProps{Value: 3, Limit: 0}, "0.00%"},
	} {
		if got := tc.p.width(); got != tc.want {
			t.Errorf("%s: MeterProps%+v.width() = %q, want %q", tc.name, tc.p, got, tc.want)
		}
	}
}

func TestMeterDrawsTheUnfilledPartAndKeepsItsCountAsText(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer
	if err := Meter(MeterProps{Value: 5, Limit: 20, Label: "5 / 20"}).Render(t.Context(), &b); err != nil {
		t.Fatalf("Meter.Render: %v", err)
	}
	got := b.String()
	svg := svgOf(t, got)
	for _, want := range []string{`class="goen-chartmeter__track"`, `class="goen-chartmeter__fill"`, `width="25.00%"`, `height="10"`, `aria-hidden="true"`} {
		if !strings.Contains(svg, want) {
			t.Errorf("Meter(5 of 20) draws %s, want it to contain %s", svg, want)
		}
	}
	if strings.Contains(svg, "<text") || strings.Contains(got, "style=") {
		t.Errorf("Meter(5 of 20) = %s, want the count outside the SVG and no style attribute", got)
	}
	if !strings.Contains(svg, `focusable="false"`) {
		t.Errorf("Meter(5 of 20) draws %s, want focusable=\"false\"", svg)
	}
	if strings.Contains(svg, "rx=") {
		t.Errorf("Meter(5 of 20) draws %s, want square ends", svg)
	}
	if !strings.HasPrefix(got, `<div class="goen-chartmeter" aria-hidden="true">`) {
		t.Errorf("Meter(5 of 20) = %s, want the meter and its count hidden from assistive technology, which reads the row's own text", got)
	}
	if !strings.Contains(got, `<span class="goen-chartmeter__label">5 / 20</span>`) {
		t.Errorf("Meter(5 of 20) = %s, want the count as text beside the meter", got)
	}
}

func TestMeterWithNothingUsedDrawsNoFill(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer
	if err := Meter(MeterProps{Value: 0, Limit: 20, Label: "0 / 20"}).Render(t.Context(), &b); err != nil {
		t.Fatalf("Meter.Render: %v", err)
	}
	if strings.Contains(b.String(), "goen-chartmeter__fill") {
		t.Errorf("Meter(0 of 20) = %s, want only the track", b.String())
	}
}

func TestMeterDrawsTheLimitLineOnlyWhenAsked(t *testing.T) {
	t.Parallel()

	for _, line := range []bool{false, true} {
		var b bytes.Buffer
		if err := Meter(MeterProps{Value: 5, Limit: 20, LimitLine: line}).Render(t.Context(), &b); err != nil {
			t.Fatalf("Meter.Render: %v", err)
		}
		if got := strings.Contains(b.String(), "goen-chartmeter__limit"); got != line {
			t.Errorf("Meter(LimitLine: %v) draws the line = %v", line, got)
		}
	}
}

func renderRangeBar(t *testing.T, p RangeBarProps) string {
	t.Helper()
	var b bytes.Buffer
	if err := RangeBar(p).Render(t.Context(), &b); err != nil {
		t.Fatalf("RangeBar(%+v).Render: %v", p, err)
	}
	return b.String()
}

func TestRangeBarPositionsAreAPercentageOfTheScale(t *testing.T) {
	t.Parallel()

	p := RangeBarProps{Max: 90}
	for _, tc := range []struct {
		v    int64
		want string
	}{
		{0, "0.00%"},
		{30, "33.33%"},
		{90, "100.00%"},
		{200, "100.00%"},
		{-5, "0.00%"},
	} {
		if got := p.position(tc.v); got != tc.want {
			t.Errorf("RangeBarProps{Max: 90}.position(%d) = %q, want %q", tc.v, got, tc.want)
		}
	}
	if got := (RangeBarProps{}).position(5); got != "0.00%" {
		t.Errorf("RangeBarProps{}.position(5) = %q, want 0.00%% on a scale of nothing", got)
	}
}

func TestRangeBarDrawsTheValueItsReachAndTheWarningLine(t *testing.T) {
	t.Parallel()

	got := renderRangeBar(t, RangeBarProps{Value: 45, High: 72, Mark: 30, Max: 90})
	svg := svgOf(t, got)
	for _, want := range []string{
		`class="goen-chartrangebar__mark" x="33.33%" y="0" width="2"`,
		`class="goen-chartrangebar__reach" x="50.00%" y="0" width="30.00%"`,
		`class="goen-chartrangebar__fill" x="0" y="0" width="50.00%"`,
		`aria-hidden="true"`, `focusable="false"`,
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("RangeBar(45 to 72, line at 30 of 90) draws %s, want it to contain %s", svg, want)
		}
	}
	for _, banned := range []string{"<text", "<line", "style="} {
		if strings.Contains(got, banned) {
			t.Errorf("RangeBar(45 to 72, line at 30 of 90) = %s, want no %s", got, banned)
		}
	}
	if !strings.HasPrefix(got, `<div class="goen-chartrangebar" aria-hidden="true">`) {
		t.Errorf("RangeBar = %s, want it hidden from assistive technology, which reads the row's own text", got)
	}
}

func TestRangeBarIsUrgentOnlyWhenAsked(t *testing.T) {
	t.Parallel()

	for _, urgent := range []bool{false, true} {
		got := renderRangeBar(t, RangeBarProps{Value: 9, High: 13, Mark: 30, Max: 90, Urgent: urgent})
		if has := strings.Contains(got, "goen-chartrangebar--urgent"); has != urgent {
			t.Errorf("RangeBar(Urgent: %t) = %s, urgent class present = %t", urgent, got, has)
		}
	}
}

func TestRangeBarWithoutALengthDrawsNoReach(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		p    RangeBarProps
	}{
		{"high is the value", RangeBarProps{Value: 10, High: 10, Mark: 30, Max: 90}},
		{"the value is at the end of the scale", RangeBarProps{Value: 90, High: 120, Mark: 30, Max: 90}},
	} {
		got := renderRangeBar(t, tc.p)
		if strings.Contains(got, "goen-chartrangebar__reach") {
			t.Errorf("%s: RangeBar(%+v) = %s, want no reach", tc.name, tc.p, got)
		}
		if !strings.Contains(got, "goen-chartrangebar__mark") {
			t.Errorf("%s: RangeBar(%+v) = %s, want the 30-day line still drawn", tc.name, tc.p, got)
		}
	}
}
