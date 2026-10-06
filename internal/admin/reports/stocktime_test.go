package reports

import (
	"testing"
	"time"
)

func TestTimeInStockCountsOnlyWhatASaleMayTake(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	day := func(n int) time.Time { return from.AddDate(0, 0, n) }
	to := day(30)

	for _, tc := range []struct {
		name          string
		stock, safety int32
		moves         []movement
		want          time.Duration
	}{
		{"no movement above safety", 9, 5, nil, 30 * 24 * time.Hour},
		{"no movement at safety", 5, 5, nil, 0},
		{
			// Rolled back, the stock at from is 3 (0 sellable): it was
			// received on day 10, so 20 days had something to sell.
			"received on day ten", 13, 3,
			[]movement{{day(10), 10}},
			20 * 24 * time.Hour,
		},
		{
			// Stock at from is 8, sold down to the safety level on day 5.
			"sold down to safety on day five", 5, 5,
			[]movement{{day(5), -3}},
			5 * 24 * time.Hour,
		},
		{
			// 8 at from, 5 on day 5, 20 on day 15, 5 on day 25.
			"out for ten days between two stretches", 5, 5,
			[]movement{{day(5), -3}, {day(15), 15}, {day(25), -15}},
			15 * 24 * time.Hour,
		},
	} {
		got := timeInStock(tc.stock, tc.safety, from, to, tc.moves)
		if got != tc.want {
			t.Errorf("%s: timeInStock(%d, %d, ...) = %v, want %v", tc.name, tc.stock, tc.safety, got, tc.want)
		}
	}
}
