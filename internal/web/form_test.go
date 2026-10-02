package web

import "testing"

func TestParseCountTakesBlankAsZeroAndRefusesTheRest(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		in   string
		want int32
		ok   bool
	}{
		{"", 0, true}, {"  ", 0, true}, {"7", 7, true}, {" 12 ", 12, true},
		{"1000000", 1_000_000, true}, {"1000001", 0, false}, {"-3", 0, false},
		{"12o", 0, false}, {"999999999999999999999999", 0, false},
	} {
		got, ok := ParseCount(tt.in)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("ParseCount(%q) = %d, %t; want %d, %t", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
