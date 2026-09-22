package money_test

import (
	"math"
	"testing"

	"github.com/koopa0/goen/internal/money"
)

func TestTWDExactRetainsEveryCent(t *testing.T) {
	for _, tc := range []struct {
		cents int64
		want  string
	}{
		{0, "NT$0"}, {50, "NT$0.50"}, {-50, "-NT$0.50"}, {1, "NT$0.01"}, {-1, "-NT$0.01"},
		{100, "NT$1"}, {-100, "-NT$1"}, {10050, "NT$100.50"}, {-10001, "-NT$100.01"},
		{math.MinInt64, "-NT$92,233,720,368,547,758.08"}, {math.MaxInt64, "NT$92,233,720,368,547,758.07"},
	} {
		if got := money.TWDExact(tc.cents); got != tc.want {
			t.Errorf("TWDExact(%d)=%q; want %q", tc.cents, got, tc.want)
		}
	}
}
