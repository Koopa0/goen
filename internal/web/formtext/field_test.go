package formtext

import (
	"strings"
	"testing"
)

// ParseCount takes blank as zero and refuses the rest.
func TestParseCount(t *testing.T) {
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

// ParseBounded keeps an unreadable or out-of-range entry distinct from the
// valid zero that a blank or a typed zero means.
func TestParseBounded(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		raw  string
		max  int32
		want int32
		ok   bool
	}{
		{name: "blank is unstated", raw: "", max: 120, ok: true},
		{name: "space is unstated", raw: "  ", max: 120, ok: true},
		{name: "typed zero", raw: "0", max: 120, ok: true},
		{name: "ordinary", raw: "24", max: 120, want: 24, ok: true},
		{name: "ceiling", raw: "120", max: 120, want: 120, ok: true},
		{name: "over ceiling", raw: "121", max: 120},
		{name: "letter typo", raw: "12o", max: 120},
		{name: "decimal", raw: "24.0", max: 120},
		{name: "negative", raw: "-3", max: 120},
		{name: "inner space", raw: "2 4", max: 120},
		{name: "non ASCII digits", raw: "١٢", max: 120},
		{name: "comma", raw: "10,000", max: 200000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := ParseBounded(tt.raw, tt.max)
			if got != tt.want || ok != tt.ok {
				t.Errorf("ParseBounded(%q, %d) = (%d, %v), want (%d, %v)",
					tt.raw, tt.max, got, ok, tt.want, tt.ok)
			}
		})
	}
}

// A search term is trimmed and cut to its bound.
func TestSearchTerm(t *testing.T) {
	t.Parallel()
	if got := SearchTerm("  koto  "); got != "koto" {
		t.Errorf("SearchTerm = %q, want the trimmed term", got)
	}
	long := strings.Repeat("字", maxSearchRunes+20)
	if got := SearchTerm(long); got != strings.Repeat("字", maxSearchRunes) {
		t.Errorf("a term of %d runes is cut to %d, got %d", maxSearchRunes+20, maxSearchRunes, len([]rune(got)))
	}
}

// A malformed count is invalid, not zero.
func TestParseCountOrInvalid(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]int32{"": 0, "5": 5, "12o": -1, "-3": -1, "1000001": -1} {
		if got := ParseCountOrInvalid(in); got != want {
			t.Errorf("ParseCountOrInvalid(%q) = %d, want %d", in, got, want)
		}
	}
}
