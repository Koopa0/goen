package shoptime_test

import (
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
)

// The no-break spaces keep a date on one line; the plain space after 年 and 月
// and after the English comma is where it may break.
func TestDateTextWritesTheExactBytes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 6, 4, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		day    time.Time
		want   string
	}{
		{"zh, this year", i18n.ZhHant, time.Date(2026, 10, 30, 4, 0, 0, 0, time.UTC), "10 月 30 日"},
		{"zh, other year", i18n.ZhHant, time.Date(2028, 10, 6, 4, 0, 0, 0, time.UTC), "2028 年 10 月 6 日"},
		{"en, this year", i18n.En, time.Date(2026, 10, 30, 4, 0, 0, 0, time.UTC), "Oct 30"},
		{"en, other year", i18n.En, time.Date(2028, 10, 30, 4, 0, 0, 0, time.UTC), "Oct 30, 2028"},
	} {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		if got := shoptime.DateText(ctx, shoptime.DateOf(tt.day, now)); got != tt.want {
			t.Errorf("%s: DateText = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestDateTimeTextAddsTheShopsClock(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 6, 4, 0, 0, 0, time.UTC)
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC) // 18:00 in Taipei
	for locale, want := range map[i18n.Locale]string{
		i18n.ZhHant: "10 月 30 日 18:00",
		i18n.En:     "Oct 30 18:00",
	} {
		if got := shoptime.DateTimeText(i18n.WithLocale(t.Context(), locale), at, now); got != want {
			t.Errorf("DateTimeText(%s) = %q, want %q", locale, got, want)
		}
	}
}

func TestLastDayIsTheDayBeforeAMidnightEnd(t *testing.T) {
	t.Parallel()

	utc := func(y int, m time.Month, d, h, min int) time.Time { return time.Date(y, m, d, h, min, 0, 0, time.UTC) }
	now := utc(2026, 6, 1, 4, 0)
	tests := []struct {
		name string
		end  time.Time
		want shoptime.Date
	}{
		{"shop midnight", utc(2026, 10, 30, 16, 0), shoptime.Date{Year: 2026, Month: time.October, Day: 30}},
		{"midnight and a second", utc(2026, 10, 30, 16, 0).Add(time.Second), shoptime.Date{Year: 2026, Month: time.October, Day: 31}},
		{"23:59:30", utc(2026, 10, 30, 15, 59).Add(30 * time.Second), shoptime.Date{Year: 2026, Month: time.October, Day: 30}},
		{"mid-day", utc(2026, 10, 30, 10, 0), shoptime.Date{Year: 2026, Month: time.October, Day: 30}},
		{"one minute past midnight", utc(2026, 10, 30, 16, 1), shoptime.Date{Year: 2026, Month: time.October, Day: 31}},
		{"23:59", utc(2026, 10, 31, 15, 59), shoptime.Date{Year: 2026, Month: time.October, Day: 31}},
		{"midnight into a new month", utc(2026, 10, 31, 16, 0), shoptime.Date{Year: 2026, Month: time.October, Day: 31}},
		{"midnight on the first", utc(2026, 11, 30, 16, 0), shoptime.Date{Year: 2026, Month: time.November, Day: 30}},
		{"midnight into a new year", utc(2026, 12, 31, 16, 0), shoptime.Date{Year: 2026, Month: time.December, Day: 31}},
		{"midnight ending the year", utc(2027, 1, 1, 16, 0), shoptime.Date{Year: 2027, Month: time.January, Day: 1, OtherYear: true}},
	}
	for _, tt := range tests {
		if got := shoptime.LastDay(tt.end, now); got != tt.want {
			t.Errorf("%s: LastDay = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestDaysBetweenCountsDays(t *testing.T) {
	t.Parallel()
	d := func(m time.Month, day int) shoptime.Date { return shoptime.Date{Year: 2026, Month: m, Day: day} }
	for _, tt := range []struct {
		name     string
		from, to shoptime.Date
		want     int
	}{
		{"same day", d(10, 9), d(10, 9), 0},
		{"next day", d(10, 9), d(10, 10), 1},
		{"across a month", d(9, 30), d(10, 2), 2},
		{"earlier", d(10, 9), d(10, 7), -2},
	} {
		if got := shoptime.DaysBetween(tt.from, tt.to); got != tt.want {
			t.Errorf("%s: DaysBetween = %d, want %d", tt.name, got, tt.want)
		}
	}
}

// A midnight end is exclusive, so its last day is the day before; a mid-day end
// is its own last day.
func TestDaysLeftCountsToTheLastDay(t *testing.T) {
	t.Parallel()
	taipei := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, taipei)
	for _, tt := range []struct {
		name   string
		endsAt time.Time
		want   int
	}{
		{"midnight end, last day tomorrow", time.Date(2026, 10, 11, 0, 0, 0, 0, taipei), 1},
		{"midnight end, last day today", time.Date(2026, 10, 10, 0, 0, 0, 0, taipei), 0},
		{"mid-day end, three days on", time.Date(2026, 10, 12, 18, 0, 0, 0, taipei), 3},
		{"ended yesterday", time.Date(2026, 10, 9, 0, 0, 0, 0, taipei), -1},
		// 17:00 UTC is already the next day in Taipei.
		{"in UTC", time.Date(2026, 10, 12, 17, 0, 0, 0, time.UTC), 4},
	} {
		if got := shoptime.DaysLeft(now, tt.endsAt); got != tt.want {
			t.Errorf("%s: DaysLeft = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestDateLabelIsTheShortForm(t *testing.T) {
	t.Parallel()
	d := shoptime.Date{Year: 2026, Month: time.October, Day: 30}
	for locale, want := range map[i18n.Locale]string{i18n.ZhHant: "10/30", i18n.En: "Oct\u00a030"} {
		if got := shoptime.DateLabel(i18n.WithLocale(t.Context(), locale), d); got != want {
			t.Errorf("DateLabel(%s) = %q, want %q", locale, got, want)
		}
	}
}

func TestStampTextIsTheShortDateAndTheShopsClock(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 3, 6, 2, 0, 0, time.UTC) // 14:02 in Taipei
	for locale, want := range map[i18n.Locale]string{
		i18n.ZhHant: "10/3 14:02",
		i18n.En:     "Oct 3 14:02",
	} {
		ctx := i18n.WithLocale(t.Context(), locale)
		if got := shoptime.StampText(ctx, at); got != want {
			t.Errorf("%v: StampText = %q, want %q", locale, got, want)
		}
	}
	if got, want := shoptime.ISOStamp(at), "2026-10-03T14:02+08:00"; got != want {
		t.Errorf("ISOStamp = %q, want %q", got, want)
	}
}
