package web

import (
	"strings"
	"testing"
)

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

func TestASearchTermIsTrimmedAndCutToItsBound(t *testing.T) {
	t.Parallel()
	if got := SearchTerm("  koto  "); got != "koto" {
		t.Errorf("SearchTerm = %q, want the trimmed term", got)
	}
	long := strings.Repeat("字", maxSearchRunes+20)
	if got := SearchTerm(long); got != strings.Repeat("字", maxSearchRunes) {
		t.Errorf("a term of %d runes is cut to %d, got %d", maxSearchRunes+20, maxSearchRunes, len([]rune(got)))
	}
}
