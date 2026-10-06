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

func TestBarDrawsSquareOverAHairlineWithItsCountAsText(t *testing.T) {
	t.Parallel()

	got := renderBar(t, BarProps{Value: 20, Max: 40, Label: "20"})
	svg := svgOf(t, got)
	for _, want := range []string{
		`class="goen-chartbar__track"`,
		`class="goen-chartbar__fill"`,
		`width="50.00%"`,
		`height="6"`,
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
	if strings.Contains(svg, "rx=") {
		t.Errorf("Bar(20 of 40) draws %s, want square ends", svg)
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

func TestBarOfZeroDrawsNoRectButKeepsItsLabel(t *testing.T) {
	t.Parallel()

	got := renderBar(t, BarProps{Value: 0, Max: 40, Label: "0"})
	if strings.Contains(got, "<rect") {
		t.Errorf("Bar(0 of 40) = %s, want no rect", got)
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

func TestRangeBarDrawsTheBarItsRangeAndTheLine(t *testing.T) {
	t.Parallel()

	got := renderRangeBar(t, RangeBarProps{Value: 45, Low: 18, High: 72, Mark: 30, Max: 90})
	svg := svgOf(t, got)
	for _, want := range []string{
		`class="goen-rangebar__fill"`, `width="50.00%"`,
		`class="goen-rangebar__range" x1="20.00%" x2="80.00%"`,
		`class="goen-rangebar__cap" x1="20.00%" x2="20.00%"`,
		`class="goen-rangebar__cap" x1="80.00%" x2="80.00%"`,
		`class="goen-rangebar__mark" x1="33.33%" x2="33.33%"`,
		`aria-hidden="true"`, `focusable="false"`,
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("RangeBar(45, 18-72, line 30 of 90) draws %s, want it to contain %s", svg, want)
		}
	}
	for _, banned := range []string{"<text", "rx=", "style="} {
		if strings.Contains(got, banned) {
			t.Errorf("RangeBar(45, 18-72, line 30 of 90) = %s, want no %s", got, banned)
		}
	}
	if !strings.HasPrefix(got, `<div class="goen-rangebar" aria-hidden="true">`) {
		t.Errorf("RangeBar = %s, want it hidden from assistive technology, which reads the row's own text", got)
	}
}

func TestRangeBarWithoutALengthDrawsNoRange(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		p    RangeBarProps
	}{
		{"low and high meet", RangeBarProps{Value: 10, Low: 10, High: 10, Mark: 30, Max: 90}},
		{"the range starts past the scale", RangeBarProps{Value: 90, Low: 95, High: 120, Mark: 30, Max: 90}},
	} {
		got := renderRangeBar(t, tc.p)
		if strings.Contains(got, "goen-rangebar__range") || strings.Contains(got, "goen-rangebar__cap") {
			t.Errorf("%s: RangeBar(%+v) = %s, want no range", tc.name, tc.p, got)
		}
		if !strings.Contains(got, "goen-rangebar__mark") {
			t.Errorf("%s: RangeBar(%+v) = %s, want the line still drawn", tc.name, tc.p, got)
		}
	}
}
