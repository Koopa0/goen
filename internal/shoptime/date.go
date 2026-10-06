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

// DateLabel is d the short way a day grid labels its ends: 10/30 or Oct 30.
func DateLabel(ctx context.Context, d Date) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyDateLabel), d.Month.String()[:3], int(d.Month), d.Day)
}

// DaysBetween is how many shop days lie from from's day to to's day: negative
// when to is the earlier day, zero within one day.
func DaysBetween(from, to time.Time) int {
	civil := func(t time.Time) time.Time {
		y, m, d := In(t).Date()
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
	return int(civil(to).Sub(civil(from)) / (24 * time.Hour))
}

// DateTimeText is DateText followed by the shop's clock time of t.
func DateTimeText(ctx context.Context, t, now time.Time) string {
	return DateText(ctx, DateOf(t, now)) + " " + In(t).Format("15:04")
}

// LastDay is the last shop day a period ending at t still runs. The end is
// exclusive, so a period ending at midnight ended the day before.
func LastDay(t, now time.Time) Date { return DateOf(t.Add(-time.Nanosecond), now) }
