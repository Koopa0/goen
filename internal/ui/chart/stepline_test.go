package chart

import (
	"bytes"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// stepProps is ten days: 10 in stock, 30 received on the fourth day, 38 from the
// sixth and 2, the safety stock, from the ninth. The axis tops out at 40, so a
// level of v is drawn at 26 + 136 * (1 - v/40).
func stepProps() StepLineProps {
	levels := []int64{10, 10, 10, 40, 40, 38, 38, 38, 2, 2}
	received := make([]int64, len(levels))
	received[3] = 30
	stock, in := days(len(levels), levels...), days(len(levels), received...)
	return StepLineProps{
		Stock:         Series{Label: "In stock", Buckets: stock},
		Received:      Series{Label: "Received", Buckets: in},
		Safety:        2,
		SafetyHeading: "Safety stock",
		Caption:       "2 in stock.",
		Note:          "Worked back from the ledger.",
		DayHeading:    "Date",
	}
}

func renderStepLine(t *testing.T, p StepLineProps) string {
	t.Helper()
	var b bytes.Buffer
	if err := StepLine(p).Render(i18n.WithLocale(t.Context(), i18n.En), &b); err != nil {
		t.Fatalf("StepLine.Render: %v", err)
	}
	return b.String()
}

func TestStepLineStepsAtTheDayTheLevelChanges(t *testing.T) {
	t.Parallel()

	got := newStepLine(t.Context(), stepProps()).Line
	want := "0.0,128.0 300.0,128.0 300.0,26.0 500.0,26.0 500.0,32.8 800.0,32.8 800.0,155.2 1000.0,155.2"
	if got != want {
		t.Errorf("newStepLine(...).Line = %q, want %q", got, want)
	}
}

func TestStepLineDrawsTheSafetyLevelAcrossTheLevelsOfEveryDay(t *testing.T) {
	t.Parallel()

	got := renderStepLine(t, stepProps())
	for _, want := range []string{
		`<line class="goen-chart__safety" x1="0" x2="100%" y1="155.2" y2="155.2"></line>`,
		`>Safety stock 2</text>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("StepLine does not contain %s", want)
		}
	}

	p := stepProps()
	p.Safety = 0
	if got := renderStepLine(t, p); strings.Contains(got, "goen-chart__safety") {
		t.Error("a safety stock of 0 draws a line, which would lie on the baseline")
	}
}

func TestStepLineAxisReachesASafetyLevelAboveEveryLevel(t *testing.T) {
	t.Parallel()

	p := stepProps()
	p.Stock.Buckets = days(10, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3)
	p.Received.Buckets = days(10)
	p.Safety = 10
	r := newStepLine(t.Context(), p)
	if r.SafetyY < receiptBand {
		t.Errorf("the safety line is at y = %v, above the plot's top at %d", r.SafetyY, receiptBand)
	}
}

func TestStepLineMarksEachReceiptAndTheTableSaysSo(t *testing.T) {
	t.Parallel()

	got := renderStepLine(t, stepProps())
	for _, want := range []string{
		`<line class="goen-chart__receipt" x1="30.00%" x2="30.00%" y1="18.0" y2="24.0"></line>`,
		`text-anchor="middle">Received +30</text>`,
		`<th scope="col">Safety stock</th><th scope="col">Received</th>`,
		`<th scope="row">Sep 10</th><td>40</td><td>2</td><td>+30</td>`,
		`<th scope="row">Sep 9</th><td>10</td><td>2</td><td></td>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("StepLine does not contain %s", want)
		}
	}
	if marks := strings.Count(got, `class="goen-chart__receipt"`); marks != 1 {
		t.Errorf("StepLine marks %d receipts, want 1", marks)
	}
}

// Two names closer than their own width would run into each other; the second
// keeps its mark, and the table has the figure.
func TestStepLineNamesOnlyReceiptsThatLeaveRoom(t *testing.T) {
	t.Parallel()

	p := stepProps()
	p.Stock.Buckets = days(40, slicesOf(40, 5)...)
	p.Received.Buckets = days(40, 0, 0, 0, 0, 0, 10, 0, 0, 0, 10, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 7)
	got := renderStepLine(t, p)
	if marks, names := strings.Count(got, `class="goen-chart__receipt"`), strings.Count(got, "Received +"); marks != 3 || names != 2 {
		t.Errorf("StepLine marks %d receipts and names %d, want 3 and 2 (days 5 and 9 are too close for two names)\n%s", marks, names, got)
	}
	if strings.Count(got, `<td>+10</td>`) != 2 {
		t.Error("the table does not give both receipts of 10")
	}
}

func slicesOf(n int, v int64) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestStepLineDrawsAnEmptyShelfAsALineOnTheBaseline(t *testing.T) {
	t.Parallel()

	p := stepProps()
	p.Stock.Buckets = days(10)
	p.Received.Buckets = days(10)
	p.Safety = 0
	got := renderStepLine(t, p)
	if strings.Contains(got, "NaN") || strings.Contains(got, "Inf") {
		t.Errorf("a level of 0 on every day is drawn at a point that is not a number:\n%s", got)
	}
	if !strings.Contains(got, `class="goen-chart__level"`) {
		t.Error("a level of 0 on every day draws no line, but it is data")
	}
}

func TestStepLineSeesNoStyleAndHidesItsDrawingFromAssistiveTechnology(t *testing.T) {
	t.Parallel()

	got := renderStepLine(t, stepProps())
	if strings.Contains(got, "style=") {
		t.Error("StepLine sets a style attribute, which the content security policy refuses")
	}
	if outer, hidden := strings.Count(got, `<svg class="goen-chart__`), strings.Count(got, `aria-hidden="true" focusable="false"`); outer != hidden || outer != 2 {
		t.Errorf("the drawing has %d outer SVGs and %d of them hidden from assistive technology, want 2 and 2", outer, hidden)
	}
	for _, want := range []string{
		`<figcaption class="goen-chart__caption">2 in stock.</figcaption>`,
		`<p class="goen-chart__note">Worked back from the ledger.</p>`,
		"<summary>Show as a table</summary>",
		`goen-chart__frame--noends`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("StepLine does not contain %s", want)
		}
	}
}

func TestStepLineWithNoDaysDrawsNothing(t *testing.T) {
	t.Parallel()

	if got := renderStepLine(t, StepLineProps{Caption: "x"}); got != "" {
		t.Errorf("StepLine with no days = %q, want nothing", got)
	}
}
