package dashboard

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/sales"
	"github.com/koopa0/goen/internal/ui/chart"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

// A latest paid order that could not be read leaves the week on the page and
// says so; any other failure leaves the week out, which the page tells apart
// from a week without orders.
func TestShowWeekKeepsTheWeekWhenOnlyLatestFails(t *testing.T) {
	t.Parallel()
	monday := time.Date(2026, time.October, 12, 0, 0, 0, 0, time.UTC)
	tuesday := monday.AddDate(0, 0, 1)
	recent := sales.Recent{
		Current: sales.Period{
			Orders: 3, RevenueCents: 90000, RevenueSquares: 2.7e9, Partial: true,
			Days: []sales.Day{{Day: monday, Orders: 1, RevenueCents: 30000}, {Day: tuesday, Orders: 2, RevenueCents: 60000}},
		},
		Previous: sales.Period{
			Orders: 1, RevenueCents: 10000, RevenueSquares: 1e8,
			Days: []sales.Day{{Day: monday.AddDate(0, 0, -7), Orders: 1, RevenueCents: 10000}},
		},
	}
	week := admin.Week{
		Orders: 3, RevenueCents: 90000, RevenueSquares: 2.7e9,
		Previous: admin.PreviousFigures{Orders: 1, RevenueCents: 10000, RevenueSquares: 1e8},
		RevenueDays: chart.SparklineProps{
			Previous: chart.Series{Buckets: []chart.Bucket{{Day: monday.AddDate(0, 0, -7), Value: 10000}}},
			Current:  chart.Series{Partial: true, Buckets: []chart.Bucket{{Day: monday, Value: 30000}, {Day: tuesday, Value: 60000}}},
		},
		OrderDays: chart.SparklineProps{
			Previous: chart.Series{Buckets: []chart.Bucket{{Day: monday.AddDate(0, 0, -7), Value: 1}}},
			Current:  chart.Series{Partial: true, Buckets: []chart.Bucket{{Day: monday, Value: 1}, {Day: tuesday, Value: 2}}},
		},
	}
	paid := recent
	paid.Latest = &sales.PaidOrder{Number: "GO-261013-ABCDEF", TotalCents: 30000, Elapsed: 90 * time.Minute}
	down := errors.New("connection reset")

	tests := []struct {
		name   string
		recent sales.Recent
		err    error
		want   admin.DashboardView
	}{
		{"read", paid, nil, admin.DashboardView{
			Week: week, Latest: &admin.LatestPaid{Number: "GO-261013-ABCDEF", TotalCents: 30000, Elapsed: 90 * time.Minute},
		}},
		{"never paid", recent, nil, admin.DashboardView{Week: week}},
		{"latest unreadable", recent, fmt.Errorf("%w: %w", sales.ErrLatestPaid, down), admin.DashboardView{
			Week: week, LatestUnavailable: true,
		}},
		{"unreadable", sales.Recent{}, fmt.Errorf("read the week's revenue: %w", down), admin.DashboardView{WeekUnavailable: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got admin.DashboardView
			showWeek(&got, tt.recent, tt.err)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("showWeek mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
