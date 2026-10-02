package home

import (
	"testing"
	"time"
)

// The department of the day follows the shop's calendar, not the server's: one
// shop day is one department for every visitor, and midnight in the shop's
// zone moves it on to the next.
func TestDayIndexRotatesOnTheShopDay(t *testing.T) {
	t.Parallel()

	// 2026-10-01 15:59 UTC is 23:59 in Taipei; a minute later is the next shop day.
	lateEvening := time.Date(2026, 10, 1, 15, 59, 0, 0, time.UTC)
	midnight := lateEvening.Add(time.Minute)
	earlyMorning := time.Date(2026, 10, 1, 16, 5, 0, 0, time.UTC)

	for _, n := range []int{2, 3, 6} {
		before, after := dayIndex(lateEvening, n), dayIndex(midnight, n)
		if after != (before+1)%n {
			t.Errorf("n=%d: the index went %d to %d across shop midnight, want the next one", n, before, after)
		}
		if got := dayIndex(earlyMorning, n); got != after {
			t.Errorf("n=%d: two moments of one shop day gave %d and %d", n, after, got)
		}
		if before < 0 || before >= n {
			t.Errorf("n=%d: index %d is out of range", n, before)
		}
	}
}

func TestDayIndexOfNothingIsZero(t *testing.T) {
	t.Parallel()
	if got := dayIndex(time.Now(), 0); got != 0 {
		t.Errorf("dayIndex with no departments = %d, want 0", got)
	}
}
