package reports

import (
	"testing"
	"time"

	"github.com/koopa0/goen/internal/shoptime"
)

// TestPeriodsWholeShopDays: periods span whole shop days, and the previous
// period stops at the same hour.
func TestPeriodsWholeShopDays(t *testing.T) {
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
		cur, prev := periods(tt.now, tt.days)
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
				t.Errorf("%s: periods(%s, %d) %s = %s, want %s", tt.name, tt.now, tt.days, c.what, got, c.want)
			}
		}
	}
}

// TestPeriodsSameSpan: both periods span as many shop days as the window.
func TestPeriodsSameSpan(t *testing.T) {
	t.Parallel()

	for _, now := range []time.Time{
		time.Date(2026, 10, 5, 7, 20, 0, 0, time.UTC),  // 15:20 in Taipei
		time.Date(2026, 10, 4, 16, 30, 0, 0, time.UTC), // 00:30 on the next shop day
		time.Date(2027, 1, 3, 2, 0, 0, 0, time.UTC),
	} {
		for _, days := range reportWindows {
			cur, prev := periods(now, int(days))
			for name, p := range map[string]period{"current": cur, "previous": prev} {
				first, last := shoptime.QueryDate(p.from), shoptime.QueryDate(p.to.Add(-time.Nanosecond))
				if got := int(last.Sub(first).Hours()/24) + 1; got != int(days) {
					t.Errorf("%s period of %d days at %s covers %d shop days (%s to %s), want %d",
						name, days, now, got, first.Format(time.DateOnly), last.Format(time.DateOnly), days)
				}
			}
		}
	}
}

// TestEndsMidDay: only a period cut mid-day ends on a partial day.
func TestEndsMidDay(t *testing.T) {
	t.Parallel()

	midnight := shoptime.Midnight(time.Date(2026, 10, 5, 7, 20, 0, 0, time.UTC))
	for _, tt := range []struct {
		name string
		to   time.Time
		want bool
	}{
		{"cut at 15:20", midnight.Add(15*time.Hour + 20*time.Minute), true},
		{"cut one minute past midnight", midnight.Add(time.Minute), true},
		{"cut at midnight, so the last day is whole", midnight, false},
	} {
		if got := endsMidDay(period{from: midnight.AddDate(0, 0, -7), to: tt.to}); got != tt.want {
			t.Errorf("%s: endsMidDay = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestWholeDays: the days a Recent lays out are the last twice as many shop
// days, from a midnight to today, whatever the minute now is; an order at
// 23:59:59 belongs to its day and one at 00:00:00 to the next.
func TestWholeDays(t *testing.T) {
	t.Parallel()

	const days = 7
	for _, tt := range []struct {
		name         string
		now          time.Time
		first, today string
	}{
		{"afternoon", time.Date(2026, 10, 5, 7, 20, 0, 0, time.UTC), "2026-09-22", "2026-10-05"},
		{"the first second of the shop day", time.Date(2026, 10, 4, 16, 0, 0, 0, time.UTC), "2026-09-22", "2026-10-05"},
		{"the last second of the shop day", time.Date(2026, 10, 5, 15, 59, 59, 0, time.UTC), "2026-09-22", "2026-10-05"},
		{"across a year", time.Date(2027, 1, 3, 2, 0, 0, 0, time.UTC), "2026-12-21", "2027-01-03"},
	} {
		span, first, last := wholeDays(tt.now, days)
		if got := shoptime.Minute(span.from); got != tt.first+" 00:00" {
			t.Errorf("%s: the span starts at %s, want %s 00:00", tt.name, got, tt.first)
		}
		if !span.to.Equal(tt.now) {
			t.Errorf("%s: the span ends at %s, want now", tt.name, span.to)
		}
		if got := first.Format(time.DateOnly); got != tt.first {
			t.Errorf("%s: the first row is %s, want %s", tt.name, got, tt.first)
		}
		if got := last.Format(time.DateOnly); got != tt.today {
			t.Errorf("%s: the last row is %s, want today %s", tt.name, got, tt.today)
		}
		if got := int(last.Sub(first).Hours()/24) + 1; got != 2*days {
			t.Errorf("%s: %d rows, want %d", tt.name, got, 2*days)
		}
	}

	// Taipei is UTC+8: 15:59:59 UTC on the 3rd is 23:59:59 on the 3rd there,
	// and 16:00:00 UTC is 00:00:00 on the 4th.
	now := time.Date(2026, 10, 5, 7, 20, 0, 0, time.UTC)
	span, first, _ := wholeDays(now, days)
	for _, tt := range []struct {
		at     time.Time
		inside bool
		day    string
	}{
		{span.from.Add(-time.Second), false, "2026-09-21"}, // the shop day before the first row, 23:59:59
		{span.from, true, "2026-09-22"},                    // the first row's first instant
		{time.Date(2026, 10, 3, 15, 59, 59, 0, time.UTC), true, "2026-10-03"},
		{time.Date(2026, 10, 3, 16, 0, 0, 0, time.UTC), true, "2026-10-04"},
		{now.Add(-time.Nanosecond), true, "2026-10-05"},
		{now, false, "2026-10-05"}, // half-open: now belongs to the next read
	} {
		if inside := !tt.at.Before(span.from) && tt.at.Before(span.to); inside != tt.inside {
			t.Errorf("an order at %s is inside the span = %v, want %v", shoptime.Second(tt.at), inside, tt.inside)
		}
		day := shoptime.QueryDate(tt.at)
		if got := day.Format(time.DateOnly); got != tt.day {
			t.Errorf("an order at %s is on shop day %s, want %s", shoptime.Second(tt.at), got, tt.day)
		}
		if tt.inside && day.Before(first) {
			t.Errorf("an order at %s falls on %s, before the first row %s", shoptime.Second(tt.at), tt.day, first.Format(time.DateOnly))
		}
	}
}
