//go:build integration

package cart_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/ui/pages"
)

// The last day of the right of return is the database's, counted in the shop's
// day: a parcel delivered at 00:30 in Taipei on the 2nd has until the 9th, and
// the store role the customer's pages read through may call it.
func TestTheStoreRoleReadsTheLastDayToReturn(t *testing.T) {
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

// The home page and the department page state the two windows from pages.RescissionDaysText and
// pages.ReturnDaysText; the database decides them.
func TestTheStatedReturnWindowsAreTheOnesTheDatabaseEnforces(t *testing.T) {
	ctx := t.Context()
	conn := storeApplicationPool(t, "stated-return-windows")
	rescission, err := strconv.Atoi(pages.RescissionDaysText())
	if err != nil {
		t.Fatalf("parse stated rescission days: %v", err)
	}
	goodwill, err := strconv.Atoi(pages.ReturnDaysText())
	if err != nil {
		t.Fatalf("parse stated return days: %v", err)
	}

	var days int
	if err := conn.QueryRow(ctx, `
		SELECT return_window_ends('2026-10-01 16:30:00+00'::timestamptz) - shop_day('2026-10-01 16:30:00+00'::timestamptz)`).Scan(&days); err != nil {
		t.Fatalf("read the rescission window: %v", err)
	}
	if days != rescission {
		t.Errorf("the shop states %d days to cancel, return_window_ends allows %d", rescission, days)
	}

	taipei := time.FixedZone("Asia/Taipei", 8*60*60)
	delivered := time.Date(2026, 10, 2, 0, 30, 0, 0, taipei)
	for _, tt := range []struct {
		daysAfter int
		want      string
	}{{goodwill, "goodwill"}, {goodwill + 1, "after"}} {
		requested := delivered.AddDate(0, 0, tt.daysAfter).Add(12 * time.Hour)
		var got string
		if err := conn.QueryRow(ctx, `SELECT return_line_policy_window($1, $2)`, requested, delivered).Scan(&got); err != nil {
			t.Fatalf("read the policy window %d days after delivery: %v", tt.daysAfter, err)
		}
		if got != tt.want {
			t.Errorf("a request %d days after the delivery day is %q, want %q (the shop states %d days)", tt.daysAfter, got, tt.want, goodwill)
		}
	}
}
