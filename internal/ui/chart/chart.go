// Package chart draws data as inline SVG. It only draws: sentences, thresholds
// and the table that is each chart's text equivalent belong to the page.
package chart

import "strconv"

// barReach is the share of the track the longest bar fills, leaving room for
// its label at the tip.
const barReach = 80.0

// minBarWidth keeps a positive value visible when it is far below the longest,
// at the price of proportionality: every value below 0.75% of Max draws the
// same length.
const minBarWidth = 0.6

// BarProps is one bar on a scale shared by its list or table: Max is the
// largest value drawn on that scale, Label the already localised text at the
// bar's tip.
type BarProps struct {
	Value int64
	Max   int64
	Label string
}

// width is the bar's length as a percentage of the track.
func (p BarProps) width() string {
	w := 0.0
	if p.Value > 0 && p.Max > 0 {
		w = max(float64(min(p.Value, p.Max))/float64(p.Max)*barReach, minBarWidth)
	}
	return strconv.FormatFloat(w, 'f', 2, 64) + "%"
}
