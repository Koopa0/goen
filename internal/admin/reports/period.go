package reports

import (
	"time"

	"github.com/koopa0/goen/internal/shoptime"
)

// span is the half-open stretch [from, to) of the shop's clock.
type span struct{ from, to time.Time }

// spans cuts the last days shop days, today included and ending now, and the
// days shop days before them, which end at the same time of day so that the
// two are compared over equal hours.
func spans(now time.Time, days int) (current, previous span) {
	today := shoptime.Midnight(now)
	current = span{from: today.AddDate(0, 0, 1-days), to: now}
	previous = span{from: today.AddDate(0, 0, 1-2*days), to: shoptime.In(now).AddDate(0, 0, -days)}
	return current, previous
}
