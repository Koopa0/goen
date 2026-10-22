//go:build integration

package checkout_test

import (
	"testing"
	"time"
)

// TestReturnWindowEnds: The store role can call return_window_ends and
// return_line_policy_window: a parcel delivered at 00:30 in Taipei on the 2nd
// has until the 9th, and a request on that day is within the window. The last
// day of the right of return is the database's, counted in the shop's day: a
// parcel delivered at 00:30 in Taipei on the 2nd has until the 9th, and the
// store role the customer's pages read through may call it.
func TestReturnWindowEnds(t *testing.T) {
	ctx := t.Context()
	conn := storeApplicationPool(t, "rescission-day")
	var got time.Time
	if err := conn.QueryRow(ctx, `
		SELECT return_window_ends('2026-10-01 16:30:00+00'::timestamptz)`).Scan(&got); err != nil {
		t.Fatalf("store role reads return_window_ends: %v", err)
	}
	if want := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("last day = %s; want %s (2 Oct in Taipei plus seven)", got.Format(time.DateOnly), want.Format(time.DateOnly))
	}
	var window string
	if err := conn.QueryRow(ctx, `
		SELECT return_line_policy_window('2026-10-09 15:00:00+00', '2026-10-01 16:30:00+00')`).Scan(&window); err != nil {
		t.Fatalf("store role reads return_line_policy_window: %v", err)
	}
	if window != "within" {
		t.Errorf("a request on the last day is %q; want within", window)
	}
}
