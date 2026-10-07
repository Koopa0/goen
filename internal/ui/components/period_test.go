package components_test

import (
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/components"
)

// shopDay is noon in Taipei on the given day of 2026-10.
func shopDay(day int) time.Time {
	return time.Date(2026, 10, day, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
}

// endOf is the exclusive end of a period whose last day is the given one:
// midnight starting the next day.
func endOf(day int) time.Time {
	return time.Date(2026, 10, day+1, 0, 0, 0, 0, time.FixedZone("CST", 8*3600))
}

func count(p components.PeriodSpec, state components.CellState) int {
	n := 0
	for _, c := range p.Cells {
		if c.State == state {
			n++
		}
	}
	return n
}

func TestDayPeriodCountsItsCells(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	tests := []struct {
		name                      string
		start, last, now          time.Time
		cells, past, today, ahead int
	}{
		{"running", shopDay(1), endOf(30), shopDay(9), 30, 8, 1, 21},
		{"not started", shopDay(12), endOf(25), shopDay(9), 14, 0, 0, 14},
		{"ended", shopDay(1), endOf(4), shopDay(9), 4, 4, 0, 0},
		{"first day", shopDay(9), endOf(11), shopDay(9), 3, 0, 1, 2},
		{"last day", shopDay(1), endOf(9), shopDay(9), 9, 8, 1, 0},
	}
	for _, tt := range tests {
		p, ok := components.DayPeriod(ctx, "t", tt.start, tt.last, tt.now)
		if !ok {
			t.Fatalf("%s: no period", tt.name)
		}
		if len(p.Cells) != tt.cells || count(p, components.CellPast) != tt.past ||
			count(p, components.CellToday) != tt.today || count(p, components.CellAhead) != tt.ahead {
			t.Errorf("%s: %d cells, %d past, %d today, %d ahead; want %d, %d, %d, %d", tt.name, len(p.Cells),
				count(p, components.CellPast), count(p, components.CellToday), count(p, components.CellAhead),
				tt.cells, tt.past, tt.today, tt.ahead)
		}
	}
}

func TestDayPeriodCellsAheadAreTheDaysLeft(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	p, _ := components.DayPeriod(ctx, "t", shopDay(1), endOf(30), shopDay(9))
	if got := count(p, components.CellAhead); got != 21 {
		t.Errorf("cells ahead = %d, want the 21 days left after today", got)
	}
	if !strings.Contains(p.Description, "with 21 days left") {
		t.Errorf("description %q does not say 21 days left", p.Description)
	}
}

func TestDayPeriodDrawsNothingOverSixtyCells(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	if _, ok := components.DayPeriod(ctx, "t", shopDay(1), endOf(1).AddDate(0, 0, 59), shopDay(9)); !ok {
		t.Error("60 cells: no period")
	}
	if _, ok := components.DayPeriod(ctx, "t", shopDay(1), endOf(1).AddDate(0, 0, 60), shopDay(9)); ok {
		t.Error("61 cells: a period")
	}
	if _, ok := components.DayPeriod(ctx, "t", shopDay(9), endOf(1), shopDay(9)); ok {
		t.Error("a last day before the first: a period")
	}
}

func TestDayPeriodLabelsOnlyItsEndsAndToday(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	p, _ := components.DayPeriod(ctx, "t", shopDay(1), endOf(30), shopDay(9))
	for i, c := range p.Cells {
		want := ""
		switch i {
		case 0:
			want = "10/1"
		case 29:
			want = "10/30"
		}
		if c.Label != want {
			t.Errorf("cell %d labelled %q, want %q", i, c.Label, want)
		}
	}
}

func TestDayPeriodSaysHowItStandsToday(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, tt := range []struct {
		name             string
		start, last, now time.Time
		want             string
	}{
		{"running", shopDay(1), endOf(30), shopDay(9), "t：10\u00a0月 1\u00a0日至 10\u00a0月 30\u00a0日，共 30 天；今天 10\u00a0月 9\u00a0日是第 9 天，還有 21 天。"},
		{"tomorrow", shopDay(1), endOf(10), shopDay(9), "t：10\u00a0月 1\u00a0日至 10\u00a0月 10\u00a0日，共 10 天；今天 10\u00a0月 9\u00a0日是第 9 天，明天結束。"},
		{"today", shopDay(1), endOf(9), shopDay(9), "t：10\u00a0月 1\u00a0日至 10\u00a0月 9\u00a0日，共 9 天；今天 10\u00a0月 9\u00a0日是第 9 天，今天結束。"},
		{"not started", shopDay(12), endOf(25), shopDay(9), "t：10\u00a0月 12\u00a0日至 10\u00a0月 25\u00a0日，共 14 天；還沒開始，今天是 10\u00a0月 9\u00a0日。"},
		{"ended", shopDay(1), endOf(4), shopDay(9), "t：10\u00a0月 1\u00a0日至 10\u00a0月 4\u00a0日，共 4 天；已結束。"},
	} {
		p, _ := components.DayPeriod(ctx, "t", tt.start, tt.last, tt.now)
		if p.Description != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, p.Description, tt.want)
		}
	}
}

func shopDate(day int) shoptime.Date {
	return shoptime.DateOf(shopDay(day), shopDay(day))
}

func TestReturnPeriodLaysFourteenCellsBetweenTheDatesItIsGiven(t *testing.T) {
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
		p, ok := components.ReturnPeriod(ctx, shopDate(6), shopDate(13), shopDate(20), shopDate(tt.today), false)
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
		if got := count(p, components.CellPast); got != tt.past {
			t.Errorf("%s: %d past cells, want %d", tt.name, got, tt.past)
		}
		if got := count(p, components.CellToday); got != tt.today1 {
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

func TestReturnPeriodRefusesDatesThatLeaveNoMark(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	if _, ok := components.ReturnPeriod(ctx, shopDate(6), shopDate(6), shopDate(20), shopDate(9), false); ok {
		t.Error("a last day on the day of receipt drew a grid")
	}
}
