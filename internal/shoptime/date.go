package shoptime

import (
	"context"
	"fmt"
	"time"

	"github.com/koopa0/goen/internal/i18n"
)

// DateText is d as the shopper reads it: 10 月 30 日 or Oct 30, with the year
// when d is outside the shop's current one.
func DateText(ctx context.Context, d Date) string {
	key := i18n.KeyShortDate
	if d.OtherYear {
		key = i18n.KeyShortDateYear
	}
	return fmt.Sprintf(i18n.T(ctx, key), d.Month.String()[:3], int(d.Month), d.Day, d.Year)
}

// DateLabel is d written short: 10/30 or Oct 30.
func DateLabel(ctx context.Context, d Date) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyDateLabel), d.Month.String()[:3], int(d.Month), d.Day)
}

// AddDays is the day n days after d. It keeps d's OtherYear, so it is for a label, which names no year.
func (d Date) AddDays(n int) Date {
	t := time.Date(d.Year, d.Month, d.Day+n, 0, 0, 0, 0, time.UTC)
	return Date{Year: t.Year(), Month: t.Month(), Day: t.Day(), OtherYear: d.OtherYear}
}

// StampText is when something happened, the short way a history reads it: 10/3 14:02 or Oct 3 14:02.
func StampText(ctx context.Context, t time.Time) string {
	return DateLabel(ctx, DateOf(t, t)) + " " + ClockText(t)
}

// ISOStamp is t as a time element's datetime reads it: 2026-10-03T14:02+08:00.
func ISOStamp(t time.Time) string { return In(t).Format("2006-01-02T15:04-07:00") }

// DaysBetween is how many days lie from from to to: negative when to is the
// earlier day.
func DaysBetween(from, to Date) int {
	civil := func(d Date) time.Time { return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC) }
	return int(civil(to).Sub(civil(from)) / (24 * time.Hour))
}

// DaysLeft is how many days remain after today up to the last day of a period
// ending at endsAt; negative once it has ended.
func DaysLeft(now, endsAt time.Time) int {
	return DaysBetween(DateOf(now, now), LastDay(endsAt, now))
}

// DateTimeText is DateText followed by the shop's clock time of t.
func DateTimeText(ctx context.Context, t, now time.Time) string {
	return DateText(ctx, DateOf(t, now)) + " " + ClockText(t)
}

// LastDay is the last shop day a period ending at t still runs. The end is
// exclusive, so a period ending at midnight ended the day before.
func LastDay(t, now time.Time) Date { return DateOf(t.Add(-time.Nanosecond), now) }

// ClockText is the shop's clock time of t.
func ClockText(t time.Time) string { return In(t).Format("15:04") }

// ISO is d as 2026-10-30, the form a time element's datetime reads.
func (d Date) ISO() string { return fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day) }
