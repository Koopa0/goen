package stock

import (
	"testing"

	"github.com/koopa0/goen/internal/shoptime"
)

func TestArrivalDateAcceptsOnlyCalendarDaysAndAllowsClearing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		raw, want string
		valid     bool
	}{
		{"", "", true}, {"  ", "", true}, {"2028-02-29", "2028-02-29", true},
		{"2027-02-29", "", false}, {"2026-10-03T10:00", "", false},
		{"0000-01-01", "", false}, {"2026-13-01", "", false}, {"infinity", "", false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			got, valid := parseArrival(tc.raw)
			if valid != tc.valid {
				t.Fatalf("valid = %v, want %v", valid, tc.valid)
			}
			if valid && arrivalInput(got) != tc.want {
				t.Errorf("date = %q, want %q", arrivalInput(got), tc.want)
			}
			if got.Valid && shoptime.Day(got.Time) != tc.want {
				t.Error("calendar date moved on the shop clock")
			}
		})
	}
}
