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

func TestBarWidthIsScaledToTheMax(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		p    BarProps
		want string
	}{
		{"the longest stops at the reach", BarProps{Value: 40, Max: 40}, "80.00%"},
		{"half of the max", BarProps{Value: 20, Max: 40}, "40.00%"},
		{"a tie is as long as the max", BarProps{Value: 7, Max: 7}, "80.00%"},
		{"tiny stays visible", BarProps{Value: 1, Max: 100000}, "0.60%"},
		{"above the max is clamped", BarProps{Value: 50, Max: 40}, "80.00%"},
		{"zero has no length", BarProps{Value: 0, Max: 40}, "0.00%"},
		{"no max has no length", BarProps{Value: 5, Max: 0}, "0.00%"},
	} {
		if got := tc.p.width(); got != tc.want {
			t.Errorf("%s: BarProps%+v.width() = %q, want %q", tc.name, tc.p, got, tc.want)
		}
	}
}

func TestBarRendering(t *testing.T) {
	t.Parallel()

	got := renderBar(t, BarProps{Value: 20, Max: 40, Label: "20"})
	for _, want := range []string{
		`class="goen-chart__hue"`,
		`width="40.00%"`,
		`class="goen-chart__value"`,
		`>20</text>`,
		`aria-hidden="true"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Bar(20 of 40) = %s, want it to contain %s", got, want)
		}
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
	if !strings.Contains(got, ">0</text>") {
		t.Errorf("Bar(0 of 40) = %s, want its label", got)
	}
}

func TestTiedBarsAreEqualAndEachLabelled(t *testing.T) {
	t.Parallel()

	a := renderBar(t, BarProps{Value: 9, Max: 9, Label: "9"})
	b := renderBar(t, BarProps{Value: 9, Max: 9, Label: "9"})
	if a != b {
		t.Errorf("tied bars differ: %s vs %s", a, b)
	}
	if !strings.Contains(a, ">9</text>") {
		t.Errorf("tied bar = %s, want its label", a)
	}
}
