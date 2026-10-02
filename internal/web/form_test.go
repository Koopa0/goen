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

func TestAMalformedCountIsInvalidAndNotZero(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]int32{"": 0, "5": 5, "12o": -1, "-3": -1, "1000001": -1} {
		if got := ParseCountOrInvalid(in); got != want {
			t.Errorf("ParseCountOrInvalid(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestASlugIsLowerCaseWordsJoinedByHyphens(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]bool{
		"koto-cable": true, "a1": true, "": false, "Koto": false, "a--b": false,
		"-a": false, "a-": false, "a b": false, "a/b": false,
	} {
		if got := ValidSlug(in); got != want {
			t.Errorf("ValidSlug(%q) = %t, want %t", in, got, want)
		}
	}
}
