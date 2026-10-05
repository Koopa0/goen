package reports

import (
	"testing"
	"time"

	"github.com/koopa0/goen/internal/shoptime"
)

func TestSpansAreWholeShopDaysAndTheSameHoursBefore(t *testing.T) {
	t.Parallel()

	// 07:20 UTC on 5 October is 15:20 in Taipei, the same shop day; 16:30 UTC
	// on the 4th is 00:30 on the 5th there.
	for _, tt := range []struct {
		name                             string
		now                              time.Time
		days                             int
		curFrom, curTo, prevFrom, prevTo string
	}{
		{
			name: "afternoon", now: time.Date(2026, 10, 5, 7, 20, 0, 0, time.UTC), days: 30,
			curFrom: "2026-09-06 00:00", curTo: "2026-10-05 15:20",
			prevFrom: "2026-08-07 00:00", prevTo: "2026-09-05 15:20",
		},
		{
			name: "half past midnight, already the next shop day in Taipei", now: time.Date(2026, 10, 4, 16, 30, 0, 0, time.UTC), days: 7,
			curFrom: "2026-09-29 00:00", curTo: "2026-10-05 00:30",
			prevFrom: "2026-09-22 00:00", prevTo: "2026-09-28 00:30",
		},
		{
			name: "across a month and a year", now: time.Date(2027, 1, 3, 2, 0, 0, 0, time.UTC), days: 90,
			curFrom: "2026-10-06 00:00", curTo: "2027-01-03 10:00",
			prevFrom: "2026-07-08 00:00", prevTo: "2026-10-05 10:00",
		},
	} {
		cur, prev := spans(tt.now, tt.days)
		for _, c := range []struct {
			what string
			got  time.Time
			want string
		}{
			{"current from", cur.from, tt.curFrom},
			{"current to", cur.to, tt.curTo},
			{"previous from", prev.from, tt.prevFrom},
			{"previous to", prev.to, tt.prevTo},
		} {
			if got := shoptime.Minute(c.got); got != c.want {
				t.Errorf("%s: spans(%s, %d) %s = %s, want %s", tt.name, tt.now, tt.days, c.what, got, c.want)
			}
		}
	}
}
