package campaigns

import (
	"testing"
	"time"

	"github.com/koopa0/goen/internal/shoptime"
)

func TestResultWindowIsWholeShopDaysAnchoredOnTheCampaign(t *testing.T) {
	t.Parallel()

	at := func(s string) time.Time {
		t.Helper()
		moment, err := shoptime.ParseSecond(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return moment
	}
	tests := []struct {
		name         string
		starts, ends string
		now          string
		ok           bool
		days         int
		first, last  string
		from, to     string
		partial      bool
	}{
		{"day 2, started mid-morning", "2026-10-04 09:30:00", "2026-10-17 23:59:00", "2026-10-05 15:20:00",
			true, 2, "2026-10-02", "2026-10-05", "2026-10-02 00:00:00", "2026-10-05 15:20:00", true},
		{"the day before it starts", "2026-10-06 00:00:00", "2026-10-12 23:59:00", "2026-10-05 23:59:00",
			false, 0, "", "", "", "", false},
		{"its first minute", "2026-10-05 00:00:00", "2026-10-12 23:59:00", "2026-10-05 00:00:00",
			true, 1, "2026-10-04", "2026-10-05", "2026-10-04 00:00:00", "2026-10-05 00:00:00", true},
		{"over, ending at midnight belongs to the day before", "2026-09-29 00:00:00", "2026-10-03 00:00:00", "2026-10-09 10:00:00",
			true, 4, "2026-09-25", "2026-10-02", "2026-09-25 00:00:00", "2026-10-03 00:00:00", false},
		{"longer than 14 days is cut at 14", "2026-09-01 00:00:00", "2026-10-30 23:59:00", "2026-10-05 10:00:00",
			true, 14, "2026-08-18", "2026-09-14", "2026-08-18 00:00:00", "2026-09-15 00:00:00", false},
	}
	for _, tt := range tests {
		got, ok := resultWindow(at(tt.starts), at(tt.ends), at(tt.now))
		if ok != tt.ok {
			t.Errorf("%s: resultWindow ok = %v, want %v", tt.name, ok, tt.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.days != tt.days || got.first.Format(time.DateOnly) != tt.first || got.last.Format(time.DateOnly) != tt.last ||
			shoptime.Second(got.from) != tt.from || shoptime.Second(got.to) != tt.to || got.partial != tt.partial {
			t.Errorf("%s: resultWindow = %d days, %s to %s, %s to %s, partial %v; want %d, %s to %s, %s to %s, %v", tt.name,
				got.days, got.first.Format(time.DateOnly), got.last.Format(time.DateOnly), shoptime.Second(got.from), shoptime.Second(got.to), got.partial,
				tt.days, tt.first, tt.last, tt.from, tt.to, tt.partial)
		}
	}
}
