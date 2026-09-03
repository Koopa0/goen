// Package shoptime renders a moment on the shop's clock.
//
// pgx hands back a timestamptz in the process's own zone, so formatting one
// directly renders in whatever TZ the host sets: Asia/Taipei on a developer's
// machine and UTC in the shipped image, which sets none. It is the Go half of
// what shop_day is in SQL, where ambient current_date is already forbidden.
//
// Not for a timestamp that belongs to somebody else. ECPay's invoice dates are
// a wall clock labelled UTC and are formatted with .UTC() at that boundary.
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

// In moves t onto the shop's clock without formatting it.
func In(t time.Time) time.Time { return t.In(location()) }

// Day is the shop's calendar day, as shop_day answers it in SQL.
func Day(t time.Time) string { return In(t).Format("2006-01-02") }

// Minute is a moment to the minute, which is what queues and timelines show.
func Minute(t time.Time) string { return In(t).Format("2006-01-02 15:04") }

// Second is a moment to the second, for the audit trail.
func Second(t time.Time) string { return In(t).Format("2006-01-02 15:04:05") }
