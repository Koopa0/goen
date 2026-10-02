// Package shoptime renders a moment on the shop's clock.
//
// pgx hands back a timestamptz in the process's own zone, so formatting one
// directly renders in whatever TZ the host sets: Asia/Taipei on a developer's
// machine and UTC in the shipped image, which sets none. It is the Go half of
// what shop_day is in SQL, where ambient current_date is already forbidden.
//
// A time ECPay reports is Taipei wall clock with no zone: it enters through
// ParseSecond and is rendered like any other shop moment.
package shoptime

import (
	"sync"
	"time"

	// The zone database, compiled in: the shipped image carries no
	// /usr/share/zoneinfo, so without this LoadLocation fails there and nowhere
	// a developer would see it.
	_ "time/tzdata"
)

// Zone is the shop's time zone, named here and in shop_day and nowhere else.
const Zone = "Asia/Taipei"

var location = sync.OnceValue(func() *time.Location {
	loc, err := time.LoadLocation(Zone)
	if err != nil {
		// Unreachable with tzdata linked in. Panic rather than fall back to
		// UTC, which is the defect this package exists to end.
		panic("shoptime: " + Zone + " is missing from the embedded zone database: " + err.Error())
	}
	return loc
})

func In(t time.Time) time.Time { return t.In(location()) }

// Day is the shop's calendar day, as shop_day answers it in SQL.
func Day(t time.Time) string { return In(t).Format("2006-01-02") }

// DayIf is Day for a timestamp that may be absent, as a nullable column is: the
// empty string when it is.
func DayIf(t time.Time, present bool) string {
	if !present {
		return ""
	}
	return Day(t)
}

// Date is a calendar day on the shop's clock, with whether its year differs
// from the shop's current one, so a caller can say the year only when it helps.
type Date struct {
	Year      int
	Month     time.Month
	Day       int
	OtherYear bool
}

// DateOf is t's shop day; now decides which year is the current one.
func DateOf(t, now time.Time) Date {
	t = In(t)
	return Date{Year: t.Year(), Month: t.Month(), Day: t.Day(), OtherYear: t.Year() != In(now).Year()}
}

// DaysSince is how many shop calendar days lie between t and now. It counts
// days on the shop's calendar, not 24-hour spans: a request filed at 23:50
// yesterday is one day old at 00:10, and one filed at 00:10 is not one day old at
// 23:50 the same day.
func DaysSince(t, now time.Time) int64 {
	a, b := In(t), In(now)
	from := time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, time.UTC)
	to := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, time.UTC)
	return int64(to.Sub(from).Hours() / 24)
}

func InputMinute(t time.Time) string { return In(t).Format("2006-01-02T15:04") }

// ParseInputMinute reads what a datetime-local field posts as a minute on the
// shop's clock.
func ParseInputMinute(s string) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02T15:04", s, location())
	return t, err == nil
}

func Minute(t time.Time) string { return In(t).Format("2006-01-02 15:04") }

func Second(t time.Time) string { return In(t).Format("2006-01-02 15:04:05") }

// ParseSecond reads a zoneless "2006-01-02 15:04:05" wall clock as the shop's
// own, for a provider that reports Taipei time without saying so.
func ParseSecond(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02 15:04:05", s, location())
}
