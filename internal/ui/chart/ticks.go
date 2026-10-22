package chart

import "time"

// maxGridLines is the most lines a value axis carries above its baseline.
const maxGridLines = 5

// axisStep is the distance between the lines of a value axis topping out at
// top, in the measure's own unit: the smallest of 1, 2, 2.5 and 5 times a power
// of ten that keeps the lines to maxGridLines. 2.5 only reads well in money;
// a count has no half items, and a step below NT$10 would be a fraction of a
// dollar.
func axisStep(top int64, m Measure) int64 {
	unit, tenths := int64(1), []int64{10, 20, 50}
	if m == MeasureMoney {
		unit, tenths = 100, []int64{10, 20, 25, 50}
	}
	for power := unit; ; power *= 10 {
		for _, t := range tenths {
			step := power * t / 10
			if t == 25 && power < 1000 {
				continue
			}
			if top <= step*maxGridLines {
				return step
			}
		}
	}
}

// gridLines is how many steps it takes to reach top, at least one.
func gridLines(top, step int64) int64 {
	return max((top+step-1)/step, 1)
}

// What the running total's day labels need, in pixels. The stylesheet fixes
// them to 12px at every text size, so only the plot's width changes with the
// zoom; the table carries the enlarged text.
const (
	// dayLabelWidth is what textWidth estimates for a five-character label,
	// "Aug 1". The two labels of the window that failed on 2026-10-22, "Jul 25"
	// and "Aug 1", measure 32.2px and 32.3px, so this is 5% above them. Reading
	// each label with textWidth would be 27% above "Jul 25" and would drop the
	// second day of a week on the narrowest plot, where it clears the first by
	// 13px.
	dayLabelWidth = 34
	// labelGap is the least space between two labels that
	// scripts/running-total-reflow.mjs accepts.
	labelGap = 3
	// minorPlot is the narrowest plot that draws every tick, minor ones too.
	// admin.css hides minor ticks from a figure of 519px or less and gives the
	// line-end names a 6.5rem column from a figure of 36rem, so a 36rem figure
	// at 16px text leaves 36 - 2.75 (the value axis) - 6.5 = 26.75rem, 428px.
	// Larger text never narrows it: the narrowest is 441px at 181% and 488px at
	// 200%.
	minorPlot = 428
	// endClear is how far from the first day's label, which starts at the left
	// edge of the plot, and from "Today"'s, which ends at the right, a tick
	// centred on its day stays, as a share of the run: that label, half of the
	// tick's own and the gap, over the narrowest plot that draws it. It is 12.6%,
	// so the earliest tick of 90 days is the 12th day after the first, of 30 days
	// the 4th, of 7 the 1st.
	endClear = (dayLabelWidth + dayLabelWidth/2.0 + labelGap) / minorPlot
	// columnEdge is the share the column chart keeps. Its first day has no label
	// when there are more than seven and nearToday keeps its ticks from "Today",
	// so it has no label to clear that endClear would measure.
	columnEdge = 0.07
)

// tickDays picks which days of a run of n get a label under the axis: the
// first, every day up to a week, every Monday up to a month and a month's first
// day beyond that, none within edge of either end, a share of the run. The last
// is not among them: the axis ends in "today".
func tickDays(days []time.Time, edge float64) []int {
	n := len(days)
	var picked []int
	for i := 1; i < n-1; i++ {
		if float64(i) < edge*float64(n) || float64(i) > (1-edge)*float64(n) {
			continue
		}
		switch d := days[i]; {
		case n <= 7,
			n <= 31 && d.Weekday() == time.Monday,
			n > 31 && d.Day() == 1:
			picked = append(picked, i)
		}
	}
	return picked
}
