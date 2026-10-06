package reports

import (
	"time"

	"github.com/koopa0/goen/internal/shoptime"
)

// period is the half-open stretch [from, to) of the shop's clock.
type period struct{ from, to time.Time }

// periods cuts the last days shop days, today included and ending now, and the
// days shop days before them, which end at the same time of day so that the
// two are compared over equal hours.
func periods(now time.Time, days int) (current, previous period) {
	current = period{from: shoptime.FirstDay(now, days), to: now}
	previous = period{from: shoptime.FirstDay(now, 2*days), to: shoptime.In(now).AddDate(0, 0, -days)}
	return current, previous
}
