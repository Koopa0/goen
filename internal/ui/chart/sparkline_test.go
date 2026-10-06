package chart

import (
	"strings"
	"testing"
)

func TestSparklineNeedsThreeDaysWithAValue(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		p    SparklineProps
		want bool
	}{
		{"nothing", SparklineProps{Previous: Series{Buckets: days(7)}, Current: Series{Buckets: days(7)}}, false},
		{"two days", SparklineProps{Previous: Series{Buckets: days(7, 1)}, Current: Series{Buckets: days(7, 1)}}, false},
		{"three days across both periods", SparklineProps{Previous: Series{Buckets: days(7, 1, 2)}, Current: Series{Buckets: days(7, 3)}}, true},
	} {
		if _, got := newSparkline(tc.p); got != tc.want {
			t.Errorf("%s: newSparkline drew = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSparklineDrawsTheUnfinishedDayAsAnOutline(t *testing.T) {
	t.Parallel()

	p := SparklineProps{
		Previous: Series{Buckets: filled(7, 4), Partial: true},
		Current:  Series{Buckets: filled(7, 4), Partial: true},
	}
	s, ok := newSparkline(p)
	if !ok || len(s.Bars) != 14 {
		t.Fatalf("newSparkline = %d bars, drawn %v, want 14", len(s.Bars), ok)
	}
	for i, b := range s.Bars {
		wantOpen := i == 6 || i == 13
		if got := strings.Contains(b.Class, "--open"); got != wantOpen {
			t.Errorf("bar %d open = %v, want %v", i, got, wantOpen)
		}
	}
	if s.TodayX != "96.43%" {
		t.Errorf("today tick at %q, want 96.43%%: under the last column", s.TodayX)
	}

	p.Current.Partial, p.Previous.Partial = false, false
	if s, _ := newSparkline(p); s.TodayX != "" || strings.Contains(s.Bars[13].Class, "--open") {
		t.Error("a finished period is drawn as unfinished")
	}
}

func TestSparklineScalesBothPeriodsTogetherAndSkipsZero(t *testing.T) {
	t.Parallel()

	p := SparklineProps{
		Previous: Series{Buckets: days(7, 100, 0, 50)},
		Current:  Series{Buckets: days(7, 1, 0, 0, 0, 0, 0, 25)},
	}
	s, _ := newSparkline(p)
	if len(s.Bars) != 4 {
		t.Fatalf("%d bars, want 4: a day without a value has none", len(s.Bars))
	}
	if s.Bars[0].Height != "26.0" || s.Bars[2].Height != "2.0" {
		t.Errorf("heights %s and %s, want 26.0 for the tallest and the 2.0 minimum for 1 of 100", s.Bars[0].Height, s.Bars[2].Height)
	}
	if !strings.Contains(s.Bars[0].Class, "previous") || !strings.Contains(s.Bars[3].Class, "current") {
		t.Errorf("classes %q and %q: the earlier period is grey, this one the data colour", s.Bars[0].Class, s.Bars[3].Class)
	}
}
