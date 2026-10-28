package dashboard

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/sales"
	"github.com/koopa0/goen/internal/ui/chart"
)

// A latest paid order that could not be read leaves the week on the page and
// says so; any other failure leaves the week out, which the page tells apart
// from a week without orders.
func TestShowWeekKeepsTheWeekWhenOnlyLatestFails(t *testing.T) {
	t.Parallel()
	monday := time.Date(2026, time.October, 12, 0, 0, 0, 0, time.UTC)
	tuesday := monday.AddDate(0, 0, 1)
	readAt := time.Date(2026, time.October, 13, 14, 32, 0, 0, time.UTC)
	recent := sales.Recent{
		Current: sales.Period{
			Orders: 3, RevenueCents: 90000, RevenueSquares: 2.7e9, Partial: true,
		},
		Previous: sales.Period{
			Orders: 1, RevenueCents: 10000, RevenueSquares: 1e8,
		},
		Days: []sales.Day{
			{Day: monday.AddDate(0, 0, -7), Orders: 1, RevenueCents: 10000},
			{Day: monday, Orders: 1, RevenueCents: 30000},
			{Day: tuesday, Orders: 2, RevenueCents: 60000},
		},
		BestSellers: []sales.BestSeller{{Slug: "mug", Name: "馬克杯", Units: 3, RevenueCents: 90000}},
	}
	week := Week{
		Orders: 3, RevenueCents: 90000, RevenueSquares: 2.7e9,
		Previous: recent.Previous,
	}
	daily := chart.Series{Partial: true, Buckets: []chart.Bucket{
		{Day: monday.AddDate(0, 0, -7), Value: 10000}, {Day: monday, Value: 30000}, {Day: tuesday, Value: 60000},
	}}
	paid := recent
	paid.Latest = &sales.PaidOrder{Number: "GO-261013-ABCDEF", TotalCents: 30000, Elapsed: 90 * time.Minute}
	down := errors.New("connection reset")

	tests := []struct {
		name   string
		recent sales.Recent
		err    error
		want   View
	}{
		{"read", paid, nil, View{
			Week: week, Daily: daily, ReadAt: readAt, BestSellers: recent.BestSellers,
			Latest: &LatestPaid{Number: "GO-261013-ABCDEF", TotalCents: 30000, Elapsed: 90 * time.Minute},
		}},
		{"never paid", recent, nil, View{Week: week, Daily: daily, ReadAt: readAt, BestSellers: recent.BestSellers}},
		{"latest unreadable", recent, fmt.Errorf("%w: %w", sales.ErrLatestPaid, down), View{
			Week: week, Daily: daily, ReadAt: readAt, BestSellers: recent.BestSellers, LatestUnavailable: true,
		}},
		{"unreadable", sales.Recent{}, fmt.Errorf("read the week's revenue: %w", down), View{WeekUnavailable: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got View
			showWeek(&got, tt.recent, readAt, tt.err)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("showWeek mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
