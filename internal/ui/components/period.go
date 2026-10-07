package components

import (
	"context"
	"fmt"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
)

// maxPeriodCells is the most cells a grid draws; a longer span is written as
// dates only.
const maxPeriodCells = 60

// CellState says where a cell lies against the present.
type CellState string

const (
	CellAhead CellState = ""
	CellPast  CellState = "past"
	CellToday CellState = "today"
)

type PeriodCell struct {
	State CellState
	// Mark ends the first of two spans and Extra cells make up the second: a
	// return window's statutory days, then the days goen adds.
	Mark  bool
	Extra bool
	// Label is the text under the cell; most cells carry none.
	Label string
}

// PeriodSpec draws a stored start and end to scale, one cell a day. The cell
// count is the data, so the markup carries no width.
type PeriodSpec struct {
	Cells []PeriodCell
	// Description is the one sentence a screen reader gets in place of the cells.
	Description string
	// TodayLabel is the word over the current cell.
	TodayLabel string
	// TodayAtStart says today is the day before the first cell, so the cells all lie ahead and
	// the tick stands at the start.
	TodayAtStart bool
}

// DayPeriod is a period of shop days from startsAt's day to the last day before
// endsAt, which is exclusive, named title. ok is false when the span has no days
// or more than a grid can draw.
func DayPeriod(ctx context.Context, title string, startsAt, endsAt, now time.Time) (PeriodSpec, bool) {
	first := shoptime.DateOf(startsAt, now)
	last := shoptime.LastDay(endsAt, now)
	total := shoptime.DaysBetween(first, last) + 1
	if total < 1 || total > maxPeriodCells {
		return PeriodSpec{}, false
	}
	// Index of today among the cells: before the first when the span has not
	// started, past the last when it has ended.
	today := shoptime.DaysBetween(first, shoptime.DateOf(now, now))

	cells := make([]PeriodCell, total)
	for i := range cells {
		switch {
		case i < today:
			cells[i].State = CellPast
		case i == today:
			cells[i].State = CellToday
		}
	}
	cells[0].Label = shoptime.DateLabel(ctx, first)
	cells[total-1].Label = shoptime.DateLabel(ctx, last)

	from, to, at := shoptime.DateText(ctx, first), shoptime.DateText(ctx, last), shoptime.DateText(ctx, shoptime.DateOf(now, now))
	n := int64(total)
	var description string
	switch left := total - 1 - today; {
	case today < 0:
		description = i18n.Count(ctx, i18n.KeyPeriodNotStarted, n, title, from, to, total, at)
	case today >= total:
		description = i18n.Count(ctx, i18n.KeyPeriodEnded, n, title, from, to, total)
	case left >= 2:
		description = i18n.Count(ctx, i18n.KeyPeriodRunning, n, title, from, to, total, at, today+1, left)
	case left == 1:
		description = i18n.Count(ctx, i18n.KeyPeriodEndsTomorrow, n, title, from, to, total, at, today+1)
	default:
		description = i18n.Count(ctx, i18n.KeyPeriodEndsToday, n, title, from, to, total, at, today+1)
	}
	return PeriodSpec{
		Cells:       cells,
		Description: description,
		TodayLabel:  i18n.T(ctx, i18n.KeyPeriodToday),
	}, true
}

// ReturnPeriod is the right to return a parcel, drawn from the day after it was received to the last day goen
// takes unused goods back: one cell a day, the mark on lastDay, the days after it the extension. The dates are
// the database's own; pickup says the parcel was collected rather than delivered. ok is false when no cell
// would lie between the two ends.
func ReturnPeriod(ctx context.Context, received, lastDay, goodwillEnd, today shoptime.Date, pickup bool) (PeriodSpec, bool) {
	total := shoptime.DaysBetween(received, goodwillEnd)
	mark := shoptime.DaysBetween(received, lastDay) - 1
	if total < 1 || total > maxPeriodCells || mark < 0 || mark >= total {
		return PeriodSpec{}, false
	}
	// The first cell is the day after receipt, so today is the cell before it on the day of receipt.
	at := shoptime.DaysBetween(received, today) - 1

	cells := make([]PeriodCell, total)
	for i := range cells {
		switch {
		case i < at:
			cells[i].State = CellPast
		case i == at:
			cells[i].State = CellToday
		}
		cells[i].Extra = i > mark
	}
	cells[mark].Mark = true
	cells[0].Label = shoptime.DateLabel(ctx, received.AddDays(1))
	cells[mark].Label = shoptime.DateLabel(ctx, lastDay)
	cells[total-1].Label = shoptime.DateLabel(ctx, goodwillEnd)

	receipt := i18n.KeyOrderReceivedOn
	if pickup {
		receipt = i18n.KeyOrderCollectedOn
	}
	got, last, end := fmt.Sprintf(i18n.T(ctx, receipt), shoptime.DateText(ctx, received)), shoptime.DateText(ctx, lastDay), shoptime.DateText(ctx, goodwillEnd)
	var description string
	switch left := shoptime.DaysBetween(today, lastDay); {
	case at < 0:
		description = fmt.Sprintf(i18n.T(ctx, i18n.KeyPeriodReturnStarts), got, last, end)
	case left > 0:
		description = i18n.Count(ctx, i18n.KeyPeriodReturnRunning, int64(left), got, last, end, shoptime.DateText(ctx, today), at+1, left)
	case left == 0:
		description = fmt.Sprintf(i18n.T(ctx, i18n.KeyPeriodReturnLastDay), got, last, end)
	case at < total:
		description = fmt.Sprintf(i18n.T(ctx, i18n.KeyPeriodReturnGoodwill), got, last, end)
	default:
		description = fmt.Sprintf(i18n.T(ctx, i18n.KeyPeriodReturnOver), got, last, end)
	}
	return PeriodSpec{
		Cells:        cells,
		Description:  description,
		TodayLabel:   i18n.T(ctx, i18n.KeyPeriodToday),
		TodayAtStart: at < 0,
	}, true
}
