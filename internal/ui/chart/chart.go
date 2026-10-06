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

// MeterProps is Value of a Limit, Label the count as already localised text.
// A caller with no limit draws no meter.
type MeterProps struct {
	Value int64
	Limit int64
	Label string
}

// width is the filled part as a percentage of the meter; a limit that is
// reached fills it whole.
func (p MeterProps) width() string {
	w := 0.0
	if p.Value > 0 && p.Limit > 0 {
		w = float64(min(p.Value, p.Limit)) / float64(p.Limit) * 100
	}
	return strconv.FormatFloat(w, 'f', 2, 64) + "%"
}
