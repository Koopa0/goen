package money_test

import (
	"math"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/money"
)

// TestTWD pins the rendering both the page and the letter now share. The
// negative rows are the reason this package exists: the two implementations it
// replaced agreed on every positive figure and disagreed on a refund.
func TestTWD(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		cents int64
		want  string
	}{
		{name: "zero", cents: 0, want: "NT$0"},
		{name: "under a dollar is not a dollar", cents: 99, want: "NT$0"},
		{name: "whole dollars", cents: 149_900, want: "NT$1,499"},
		{name: "no separator below a thousand", cents: 99_900, want: "NT$999"},
		{name: "first separator", cents: 100_000, want: "NT$1,000"},
		{name: "two separators", cents: 1_234_567_00, want: "NT$1,234,567"},
		{name: "cents are dropped, never rounded up", cents: 199_999, want: "NT$1,999"},

		// The sign leads the symbol. A grouping loop fed a signed string puts
		// the separator relative to the minus and produces "NT$-,123,456".
		{name: "negative", cents: -20_000, want: "-NT$200"},
		{name: "negative crossing a separator", cents: -12_345_678, want: "-NT$123,456"},
		{name: "negative under a dollar", cents: -99, want: "NT$0"},

		// Negating before dividing leaves the most negative int64 negative,
		// and its minus sign then reaches the grouping loop as a digit.
		{name: "most negative int64 does not overflow", cents: math.MinInt64,
			want: "-NT$92,233,720,368,547,758"},
		{name: "most positive int64", cents: math.MaxInt64,
			want: "NT$92,233,720,368,547,758"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := money.TWD(tt.cents); got != tt.want {
				t.Errorf("TWD(%d) = %q, want %q", tt.cents, got, tt.want)
			}
		})
	}
}

// TestTWDNeverEmitsAStraySeparator is the property the table cannot state: no
// rendering may begin or end with a separator, or place one against the sign.
// That is what a signed string fed through the grouping loop produces, and it
// is what shipped in the mail formatter.
func TestTWDNeverEmitsAStraySeparator(t *testing.T) {
	t.Parallel()

	for _, cents := range []int64{
		0, 1, 99, 100, 99_999, 100_000, -1, -99, -100, -99_999, -100_000,
		-1_000_000, 999_999_99, -999_999_99, math.MinInt64, math.MaxInt64,
	} {
		got := money.TWD(cents)
		digits, ok := strings.CutPrefix(strings.TrimPrefix(got, "-"), "NT$")
		if !ok {
			t.Errorf("TWD(%d) = %q, which is not an NT$ amount", cents, got)
			continue
		}
		if strings.HasPrefix(digits, ",") || strings.HasSuffix(digits, ",") {
			t.Errorf("TWD(%d) = %q has a separator at an edge", cents, got)
		}
		if strings.Contains(digits, ",,") {
			t.Errorf("TWD(%d) = %q has two separators together", cents, got)
		}
		for _, group := range strings.Split(digits, ",")[1:] {
			if len(group) != 3 {
				t.Errorf("TWD(%d) = %q has a %d-digit group after the first",
					cents, got, len(group))
			}
		}
	}
}
