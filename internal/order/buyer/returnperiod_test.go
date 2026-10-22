package buyer

import (
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/widget"
)

// shopDay is noon in Taipei on the given day of 2026-10.
func shopDay(day int) time.Time {
	return time.Date(2026, 10, day, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
}

func count(p widget.PeriodSpec, state widget.CellState) int {
	n := 0
	for _, c := range p.Cells {
		if c.State == state {
			n++
		}
	}
	return n
}

func shopDate(day int) shoptime.Date {
	return shoptime.DateOf(shopDay(day), shopDay(day))
}

// TestReturnPeriodCells: returnPeriod lays fourteen cells, marks the seventh as
// the last statutory day, flags cells eight to fourteen as the extension,
// labels the first, seventh and last with their dates and counts the past and
// today cells, for a day three days in, the day of receipt and a day after the
// goodwill.
func TestReturnPeriodCells(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	tests := []struct {
		name                      string
		today                     int
		past, today1, wantAtStart int
	}{
		{"three days in", 9, 2, 1, 0},
		{"the day of receipt", 6, 0, 0, 0},
		{"after the goodwill", 25, 14, 0, 0},
	}
	for _, tt := range tests {
		p, ok := returnPeriod(ctx, shopDate(6), shopDate(13), shopDate(20), shopDate(tt.today), false)
		if !ok {
			t.Fatalf("%s: no period", tt.name)
		}
		if len(p.Cells) != 14 {
			t.Fatalf("%s: %d cells, want 14", tt.name, len(p.Cells))
		}
		for i, c := range p.Cells {
			if c.Mark != (i == 6) {
				t.Errorf("%s: cell %d mark = %v; the mark belongs on cell 7", tt.name, i+1, c.Mark)
			}
			if c.Extra != (i >= 7) {
				t.Errorf("%s: cell %d extension = %v; cells 8 to 14 are the extension", tt.name, i+1, c.Extra)
			}
		}
		if got := count(p, widget.CellPast); got != tt.past {
			t.Errorf("%s: %d past cells, want %d", tt.name, got, tt.past)
		}
		if got := count(p, widget.CellToday); got != tt.today1 {
			t.Errorf("%s: %d today cells, want %d", tt.name, got, tt.today1)
		}
		if p.Cells[0].Label != "10/7" || p.Cells[6].Label != "10/13" || p.Cells[13].Label != "10/20" {
			t.Errorf("%s: labels %q %q %q, want 10/7 10/13 10/20", tt.name, p.Cells[0].Label, p.Cells[6].Label, p.Cells[13].Label)
		}
		if p.TodayAtStart != (tt.today == 6) {
			t.Errorf("%s: TodayAtStart = %v", tt.name, p.TodayAtStart)
		}
	}
}

// TestReturnPeriodNoMark: returnPeriod draws no grid when the last day falls on
// the day of receipt, which leaves no cell to mark.
func TestReturnPeriodNoMark(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	if _, ok := returnPeriod(ctx, shopDate(6), shopDate(6), shopDate(20), shopDate(9), false); ok {
		t.Error("a last day on the day of receipt drew a grid")
	}
}
