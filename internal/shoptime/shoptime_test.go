package shoptime_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/shoptime"
)

// TestTheShopsClockDoesNotFollowTheProcess is the whole point: the rendering
// must be the same on a developer's machine in Taipei and in the shipped image,
// which sets no TZ and is therefore UTC. Before this package the two differed
// by eight hours and only the second one was ever deployed.
//
// The instant chosen is 2026-09-03T20:30:00Z, which is 2026-09-04 04:30 in
// Taipei — so it crosses a DAY boundary as well as an hour, and a fixture that
// only crossed an hour would let a Day() that forgot the zone pass.
func TestTheShopsClockDoesNotFollowTheProcess(t *testing.T) {
	instant := time.Date(2026, 9, 3, 20, 30, 0, 0, time.UTC)

	for _, tt := range []struct {
		name string
		in   time.Time
	}{
		{name: "read as UTC, which is the shipped image", in: instant},
		{name: "read on a machine in Taipei", in: instant.In(mustLoad(t, "Asia/Taipei"))},
		{name: "read on a machine somewhere else entirely", in: instant.In(mustLoad(t, "America/New_York"))},
		{name: "read with a fixed offset carrying no name", in: instant.In(time.FixedZone("", -5*3600))},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := shoptime.Day(tt.in), "2026-09-04"; got != want {
				t.Errorf("Day() = %q, want %q — the shop's day, not the process's", got, want)
			}
			if got, want := shoptime.Minute(tt.in), "2026-09-04 04:30"; got != want {
				t.Errorf("Minute() = %q, want %q", got, want)
			}
			if got, want := shoptime.Second(tt.in), "2026-09-04 04:30:00"; got != want {
				t.Errorf("Second() = %q, want %q", got, want)
			}
		})
	}
}

// TestTheZoneIsTheOneTheSchemaUses guards the pair that has to agree. shop_day
// pins Asia/Taipei in SQL; a Go half on a different zone would put the page and
// the statutory window it renders on two calendars.
func TestTheZoneIsTheOneTheSchemaUses(t *testing.T) {
	t.Parallel()

	if shoptime.Zone != "Asia/Taipei" {
		t.Fatalf("Zone = %q, want Asia/Taipei to match shop_day", shoptime.Zone)
	}
	// +08:00 year-round: Taiwan has observed no daylight saving since 1979, so
	// an offset that moves means the embedded database was read wrongly.
	for _, month := range []time.Month{time.January, time.July} {
		at := shoptime.In(time.Date(2026, month, 15, 12, 0, 0, 0, time.UTC))
		if _, offset := at.Zone(); offset != 8*3600 {
			t.Errorf("%s offset = %ds, want 28800", month, offset)
		}
	}
}

// TestTheZoneDatabaseIsInTheBinary is what makes the rest of this package true
// in the shipped image. cgr.dev/chainguard/static carries no
// /usr/share/zoneinfo, so a build that dropped the time/tzdata import would
// panic on the first page served and pass every test beforehand.
//
// It asserts the IMPORT and not a LoadLocation call, and that is the whole
// design. The first version of this test loaded an obscure zone and checked it
// resolved — which passes on any developer machine whether or not the database
// is embedded, because time.LoadLocation reads /usr/share/zoneinfo first and
// only falls back to the embedded copy. Deleting the import left it green, so
// it was a test that could not fail for the reason it claimed.
func TestTheZoneDatabaseIsInTheBinary(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("shoptime.go")
	if err != nil {
		t.Fatalf("read the package source: %v", err)
	}
	if !strings.Contains(string(src), `_ "time/tzdata"`) {
		t.Error("time/tzdata is not imported: LoadLocation will fail in the " +
			"shipped image, which carries no /usr/share/zoneinfo, and nowhere else")
	}
}

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loc
}
