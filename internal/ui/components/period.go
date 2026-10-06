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

// PeriodUnit is what one cell of a period stands for.
type PeriodUnit string

const (
	PeriodMinute PeriodUnit = "minute"
	PeriodDay    PeriodUnit = "day"
	PeriodMonth  PeriodUnit = "month"
)

// CellState says where a cell lies against the present.
type CellState string

const (
	CellAhead CellState = ""
	CellPast  CellState = "past"
	CellToday CellState = "today"
)

type PeriodCell struct {
	State CellState
	// Mark is the cell where an action must have started; Extra cells come
	// after it and are drawn as the span the mark leaves open.
	Mark  bool
	Extra bool
	// Label is the text under the cell; most cells carry none.
	Label string
}

// PeriodSpec draws a stored start and end to scale, one cell per unit. The cell
// count is the data, so the markup carries no width.
type PeriodSpec struct {
	Unit  PeriodUnit
	Cells []PeriodCell
	// Description is the one sentence a screen reader gets in place of the cells.
	Description string
	// TodayLabel is the word over the current cell.
	TodayLabel string
}

func (p PeriodSpec) unitAttr() string {
	if p.Unit == PeriodDay {
		return ""
	}
	return string(p.Unit)
}

// DayPeriod is a period of shop days from startsAt's day to the last day before
// endsAt, which is exclusive, named title. ok is false when the span has no days
// or more than a grid can draw.
func DayPeriod(ctx context.Context, title string, startsAt, endsAt, now time.Time) (PeriodSpec, bool) {
	lastDay := endsAt.Add(-time.Nanosecond)
	total := shoptime.DaysBetween(startsAt, lastDay) + 1
	if total < 1 || total > maxPeriodCells {
		return PeriodSpec{}, false
	}
	// Index of today among the cells: before the first when the span has not
	// started, past the last when it has ended.
	today := shoptime.DaysBetween(startsAt, now)

	cells := make([]PeriodCell, total)
	for i := range cells {
		switch {
		case i < today:
			cells[i].State = CellPast
		case i == today:
			cells[i].State = CellToday
		}
	}
	first := shoptime.DateOf(startsAt, now)
	last := shoptime.LastDay(endsAt, now)
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
		Unit:        PeriodDay,
		Cells:       cells,
		Description: description,
		TodayLabel:  i18n.T(ctx, i18n.KeyPeriodToday),
	}, true
}

// MinutePeriod is a stock hold drawn from placedAt to until, one cell per minute.
// startBy is the minute payment must have started by; it is zero when the page
// shows no such deadline, and the cells after it are the extra span. lapsed
// fills every cell: the hold has ended. ok is false when the hold is longer
// than a grid can draw or shorter than one minute.
func MinutePeriod(ctx context.Context, placedAt, startBy, until time.Time, lapsed bool) (PeriodSpec, bool) {
	total := int(until.Sub(placedAt) / time.Minute)
	if total < 1 || total > maxPeriodCells {
		return PeriodSpec{}, false
	}
	cells := make([]PeriodCell, total)
	if lapsed {
		for i := range cells {
			cells[i].State = CellPast
		}
	}
	placed, deadline, end := shoptime.Clock(placedAt), shoptime.Clock(startBy), shoptime.Clock(until)
	cells[0].Label = placed
	cells[total-1].Label = end
	if mark := int(startBy.Sub(placedAt) / time.Minute); !startBy.IsZero() && mark > 0 && mark < total-1 {
		cells[mark].Mark = true
		cells[mark].Label = deadline
		for i := mark + 1; i < total; i++ {
			cells[i].Extra = true
		}
	}
	var description string
	switch {
	case lapsed:
		description = fmt.Sprintf(i18n.T(ctx, i18n.KeyPeriodHoldLapsed), placed, deadline, end)
	case startBy.IsZero():
		description = fmt.Sprintf(i18n.T(ctx, i18n.KeyPeriodHoldResumed), placed, end)
	default:
		description = fmt.Sprintf(i18n.T(ctx, i18n.KeyPeriodHoldOpen), placed, deadline, end)
	}
	return PeriodSpec{Unit: PeriodMinute, Cells: cells, Description: description}, true
}
