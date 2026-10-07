// Package chart draws data as inline SVG. It only draws: sentences, thresholds
// and the table that is each chart's text equivalent belong to the page.
package chart

import (
	"strconv"
	"time"
)

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

// Bucket is one shop day's value: Day is the date as SQL returns a date column,
// Value the day's amount in cents, or its count.
type Bucket struct {
	Day   time.Time
	Value int64
}

// Series is a run of consecutive shop days under one legend label. Partial says
// that the last bucket is a day still going, or cut at a time of day, so that
// it is drawn as not yet final.
type Series struct {
	Label   string
	Buckets []Bucket
	Partial bool
}

// Density is how many of a series' buckets are not zero, which is what decides
// whether there is enough to draw and how much to label.
type Density int

const (
	DensityNone   Density = iota // no bucket
	DensityFew                   // one or two
	DensitySparse                // three to six
	DensityFull                  // seven or more
)

// Density counts the buckets that are not zero.
func (s Series) Density() Density {
	filled := 0
	for _, b := range s.Buckets {
		if b.Value != 0 {
			filled++
		}
	}
	switch {
	case filled == 0:
		return DensityNone
	case filled < 3:
		return DensityFew
	case filled < 7:
		return DensitySparse
	}
	return DensityFull
}

// Measure is what a value counts, which sets the steps of its axis and how it
// reads.
type Measure int

const (
	MeasureCount Measure = iota
	MeasureMoney         // cents
)

// MeterProps is Value of a Limit, Label the count as already localised text.
// A caller with no limit draws no meter. LimitLine marks the limit with a line
// at the end of the track, for a limit that is a threshold to cross.
type MeterProps struct {
	Value     int64
	Limit     int64
	Label     string
	LimitLine bool
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

// RangeBarProps is one value and the farthest it may reach, with a line drawn
// at Mark, all numbers on the scale 0 to Max that the page chose
// and every row shares. Values beyond Max are drawn at its end. Urgent colours
// the value as a warning; the row's own text says so too, and gives the nearer
// end of the range, which is not drawn.
type RangeBarProps struct {
	Value, High, Mark, Max int64
	Urgent                 bool
}

// position is v as a percentage of the track, held within it.
func (p RangeBarProps) position(v int64) string {
	return strconv.FormatFloat(p.percent(v), 'f', 2, 64) + "%"
}

func (p RangeBarProps) percent(v int64) float64 {
	if p.Max <= 0 {
		return 0
	}
	return float64(min(max(v, 0), p.Max)) / float64(p.Max) * 100
}

// length is the stretch of the track from one value to another.
func (p RangeBarProps) length(from, to int64) string {
	return strconv.FormatFloat(p.percent(to)-p.percent(from), 'f', 2, 64) + "%"
}

// ranged reports whether the range reaches past the value on the scale.
func (p RangeBarProps) ranged() bool {
	return p.Max > 0 && min(p.High, p.Max) > min(max(p.Value, 0), p.Max)
}
