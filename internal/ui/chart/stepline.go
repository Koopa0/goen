package chart

import (
	"context"
	"strconv"
	"strings"
)

const (
	receiptBand     = 26 // room above the plot for the receipts' marks and names
	receiptNameGap  = 16 // the fewest days between two named receipts
	receiptNameEdge = 12 // within this many percent of an end, a name lies inward
)

// StepLineProps is a stock level counted at the end of each shop day and held
// until the next, with a mark where goods were received and a line at the
// safety level. Stock and Received run over the same days; their labels head
// the table's columns, and Received's names the marks. SafetyHeading heads the
// safety level's column and names its line. Caption, Note and DayHeading are the
// page's sentences, already localised.
type StepLineProps struct {
	Stock, Received Series
	Safety          int64
	SafetyHeading   string
	Caption, Note   string
	DayHeading      string
}

type receiptMark struct {
	X      string
	Anchor string
	Name   string
	Named  bool
}

type stepRow struct {
	Heading, Stock, Safety, Received string
}

// stepLine is everything StepLine draws, worked out.
type stepLine struct {
	axes
	Line        string
	ShowsSafety bool
	SafetyY     float64
	SafetyName  string
	Receipts    []receiptMark
	Rows        []stepRow
}

func newStepLine(ctx context.Context, p StepLineProps) stepLine {
	levels := p.Stock.Buckets
	n := len(levels)

	top := p.Safety
	for _, b := range levels {
		top = max(top, b.Value)
	}
	ax, y := newAxes(ctx, top, MeasureCount, levels, receiptBand)
	r := stepLine{axes: ax, ShowsSafety: p.Safety > 0, SafetyY: y(p.Safety)}
	r.SafetyName = p.SafetyHeading + " " + strconv.FormatInt(p.Safety, 10)

	x := func(i int) float64 { return float64(i) * 1000 / float64(n) }
	var line strings.Builder
	for i, b := range levels {
		if i > 0 && b.Value == levels[i-1].Value {
			continue
		}
		if i > 0 {
			line.WriteString(" " + px(x(i)) + "," + px(y(levels[i-1].Value)) + " ")
		}
		line.WriteString(px(x(i)) + "," + px(y(b.Value)))
	}
	line.WriteString(" " + px(x(n)) + "," + px(y(levels[n-1].Value)))
	r.Line = line.String()

	lastNamed := -receiptNameGap
	for i, b := range p.Received.Buckets {
		if b.Value <= 0 {
			continue
		}
		mark := receiptMark{X: percent(float64(i) * 100 / float64(n)), Anchor: "middle", Named: i-lastNamed >= receiptNameGap}
		switch {
		case i*100 < receiptNameEdge*n:
			mark.Anchor = "start"
		case i*100 > (100-receiptNameEdge)*n:
			mark.Anchor = "end"
		}
		if mark.Named {
			lastNamed = i
			mark.Name = p.Received.Label + " +" + strconv.FormatInt(b.Value, 10)
		}
		r.Receipts = append(r.Receipts, mark)
	}

	for i, b := range levels {
		row := stepRow{Heading: axisDay(ctx, b.Day), Stock: strconv.FormatInt(b.Value, 10), Safety: strconv.FormatInt(p.Safety, 10)}
		if i < len(p.Received.Buckets) && p.Received.Buckets[i].Value > 0 {
			row.Received = "+" + strconv.FormatInt(p.Received.Buckets[i].Value, 10)
		}
		r.Rows = append(r.Rows, row)
	}
	return r
}
