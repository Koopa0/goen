package chart

import "time"

// maxGridLines is the most lines a value axis carries above its baseline.
const maxGridLines = 5

// axisStep is the distance between the lines of a value axis topping out at
// top, in the measure's own unit: the smallest of 1, 2, 2.5 and 5 times a power
// of ten that keeps the lines to maxGridLines. 2.5 only reads well in money;
// a count has no half items.
func axisStep(top int64, m Measure) int64 {
	unit, tenths := int64(1), []int64{10, 20, 50}
	if m == MeasureMoney {
		unit, tenths = 100, []int64{10, 20, 25, 50}
	}
	for power := unit; ; power *= 10 {
		for _, t := range tenths {
			if step := power * t / 10; top <= step*maxGridLines {
				return step
			}
		}
	}
}

// gridLines is how many steps it takes to reach top, at least one.
func gridLines(top, step int64) int64 {
	return max((top+step-1)/step, 1)
}

// tickDays picks which days of a run of n get a label under the axis: the
// first, every day up to a week, every Monday up to a month and a month's first
// day beyond that, none too near either end. The last is not among them: the
// axis ends in "today".
func tickDays(days []time.Time) []int {
	n := len(days)
	var picked []int
	for i := 1; i < n-1; i++ {
		if i*100 < 7*n || i*100 > 93*n {
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
