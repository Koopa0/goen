// Package chart draws data as inline SVG. It only draws: sentences, thresholds
// and the table that is each chart's text equivalent belong to the page.
package chart

import "strconv"

// minBarWidth keeps a positive value visible when it is far below the longest,
// at the price of proportionality: every value below 0.6% of Max draws the
// same length.
const minBarWidth = 0.6

// wideScale is the first Max whose count no longer fits the narrow count
// column's three digits.
const wideScale = 1000

// BarProps is one bar on a scale shared by its list or table: Max is the
// largest value drawn on that scale, Label the value as already localised text,
// shown beside the bar.
type BarProps struct {
	Value int64
	Max   int64
	Label string
}

// width is the bar's length as a percentage of the track.
func (p BarProps) width() string {
	w := 0.0
	if p.Value > 0 && p.Max > 0 {
		w = max(float64(min(p.Value, p.Max))/float64(p.Max)*100, minBarWidth)
	}
	return strconv.FormatFloat(w, 'f', 2, 64) + "%"
}

// wide depends on Max alone, so every row of a scale gets the same count
// column and the same track.
func (p BarProps) wide() bool { return p.Max >= wideScale }

// RangeBarProps is one value, the range it may lie in and a reference line,
// all numbers on the scale 0 to Max that the page chose and every row shares.
// Values beyond Max are drawn at its end.
type RangeBarProps struct {
	Value, Low, High, Mark, Max int64
}

// position is v as a percentage of the track, held within it.
func (p RangeBarProps) position(v int64) string {
	pct := 0.0
	if p.Max > 0 {
		pct = float64(min(max(v, 0), p.Max)) / float64(p.Max) * 100
	}
	return strconv.FormatFloat(pct, 'f', 2, 64) + "%"
}

// ranged reports whether the range has any length on the scale.
func (p RangeBarProps) ranged() bool {
	return p.Max > 0 && min(p.High, p.Max) > max(p.Low, 0)
}
