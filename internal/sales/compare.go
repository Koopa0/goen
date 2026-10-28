package sales

import (
	"context"
	"math"

	"github.com/koopa0/goen/internal/i18n"
)

// MinOrdersForRate is the fewest orders a period needs before its figures are
// set against another's as a percentage: below it, one order moves the
// completion rate by more than five points and the paid-order and revenue
// comparisons are noise.
const MinOrdersForRate = 20

// Comparison is a figure over a run of Days shop days set against the same
// figure over the run of as many days before it.
type Comparison struct {
	Days                   int
	Orders, PreviousOrders int64
	Current, Previous      int64
	// Noise is the variance of Current - Previous.
	Noise float64
	// PreviousText is the previous figure as it is written when no percentage is.
	PreviousText string
	// AtSameTime is set when the previous run is cut at the same time of day as
	// this one, whose last day is not over: the sentence says so, because the
	// whole days drawn beside it do not add up to the figure it states.
	AtSameTime bool
}

// wording is the five sentences a comparison can be.
type wording struct{ none, previous, more, less, same i18n.Key }

var (
	wholeRuns = wording{
		i18n.KeyAdminRepNoPrevious, i18n.KeyAdminRepPrevious, i18n.KeyAdminRepMore, i18n.KeyAdminRepLess, i18n.KeyAdminRepSame,
	}
	runsAtSameTime = wording{
		i18n.KeyAdminRepNoPreviousAtTime, i18n.KeyAdminRepPreviousAtTime, i18n.KeyAdminRepMoreAtTime, i18n.KeyAdminRepLessAtTime, i18n.KeyAdminRepSameAtTime,
	}
)

// Sentence says by what percentage the figure differs from the previous run's
// only when both runs have orders enough for a percentage and the difference
// is at least two standard errors; otherwise it states the previous figure.
func (c Comparison) Sentence(ctx context.Context) string {
	days := int64(c.Days)
	words := wholeRuns
	if c.AtSameTime {
		words = runsAtSameTime
	}
	if c.PreviousOrders == 0 {
		return i18n.Count(ctx, words.none, days, c.Days)
	}
	diff := c.Current - c.Previous
	if c.Previous == 0 || c.Orders < MinOrdersForRate || c.PreviousOrders < MinOrdersForRate ||
		math.Abs(float64(diff)) < 2*math.Sqrt(c.Noise) {
		return i18n.Count(ctx, words.previous, days, c.Days, c.PreviousText)
	}
	percent := (abs(diff)*200 + c.Previous) / (c.Previous * 2)
	switch {
	case percent == 0:
		return i18n.Count(ctx, words.same, days, c.Days)
	case diff > 0:
		return i18n.Count(ctx, words.more, days, c.Days, percent)
	}
	return i18n.Count(ctx, words.less, days, c.Days, percent)
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
