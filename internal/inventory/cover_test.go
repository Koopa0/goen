package inventory

import (
	"fmt"
	"testing"
	"time"
)

const stockedAllWindow = 30 * 24 * time.Hour

func TestEstimateStatesAndDays(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		row  Sales
		want CoverState
	}{
		{"sold out whatever it sold", Sales{Sellable: 0, Sold: 400, Orders: 90, InStock: stockedAllWindow}, CoverSoldOut},
		{"a negative sellable is sold out", Sales{Sellable: -2}, CoverSoldOut},
		{"nine orders are too few", Sales{Sellable: 5, Sold: 90, Orders: 9, InStock: stockedAllWindow}, CoverFewOrders},
		{"ten orders of one unit each are enough", Sales{Sellable: 5, Sold: 10, Orders: 10, InStock: stockedAllWindow}, CoverEstimated},
		{"a short time in stock is still estimated", Sales{Sellable: 5, Sold: 40, Orders: 20, InStock: 3 * 24 * time.Hour}, CoverEstimated},
	} {
		if got := tc.row.Estimate().State; got != tc.want {
			t.Errorf("%s: %+v.Estimate().State = %d, want %d", tc.name, tc.row, got, tc.want)
		}
	}
}

// Ten orders of ten units are 100 units: counted in units the SKU would
// clear the threshold with one order.
func TestEstimateCountsOrdersNotUnits(t *testing.T) {
	t.Parallel()

	r := Sales{Sellable: 50, Sold: 100, Orders: 1, InStock: stockedAllWindow}
	if got := r.Estimate().State; got != CoverFewOrders {
		t.Errorf("one order of 100 units: state %d, want CoverFewOrders %d", got, CoverFewOrders)
	}
}

func TestEstimateDividesByTheDaysInStock(t *testing.T) {
	t.Parallel()

	// 30 units over 15 days in stock is 2 a day: 40 sellable last 20 days. Over
	// 30 calendar days it would be 1 a day and read 40.
	r := Sales{Sellable: 40, Sold: 30, Orders: 15, InStock: 15 * 24 * time.Hour}
	if got := r.Estimate().Days; got != 20 {
		t.Errorf("%+v.Estimate().Days = %d, want 20", r, got)
	}
}

// The 90% interval of 10 orders is 0.54 to 1.70 times the observed rate
// (analytics.md, Poisson table), so 0.59 to 1.84 times the days.
func TestEstimateRangeMatchesThePoissonTable(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		orders    int64
		low, high float64
		tolerance float64
	}{
		{10, 0.59, 1.84, 0.02},
		{20, 0.69, 1.51, 0.02},
		{30, 0.74, 1.39, 0.02},
	} {
		r := Sales{Sellable: 1000, Sold: tc.orders * 2, Orders: tc.orders, InStock: stockedAllWindow}
		// 1000 sellable at 2 units per order over 30 days.
		c := r.Estimate()
		days := float64(c.Days)
		if got := float64(c.Low) / days; got < tc.low-tc.tolerance || got > tc.low+tc.tolerance {
			t.Errorf("%d orders: low is %.2f of the estimate, want %.2f", tc.orders, got, tc.low)
		}
		if got := float64(c.High) / days; got < tc.high-tc.tolerance || got > tc.high+tc.tolerance {
			t.Errorf("%d orders: high is %.2f of the estimate, want %.2f", tc.orders, got, tc.high)
		}
	}
}

func TestRankKeepsTwoGroupsEachWithItsOwnCap(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	rows := make([]Risk, 0, 2*RankRows+4)
	for i := range RankRows + 2 {
		rows = append(rows, Risk{SKU: fmt.Sprintf("OUT-%02d", i), SoldOutAt: at.Add(time.Duration(i) * time.Hour)})
	}
	for i := range RankRows + 1 {
		rows = append(rows, Risk{SKU: fmt.Sprintf("EST-%02d", i), Sales: Sales{Sellable: int32(10 + i), Sold: 60, Orders: 30, InStock: stockedAllWindow}})
	}
	rows = append(rows, Risk{SKU: "FEW", Sales: Sales{Sellable: 1, Sold: 2, Orders: 2, InStock: stockedAllWindow}})

	listed, more := Rank(rows)
	var soldOut, estimated []string
	for _, r := range listed {
		switch r.Estimate().State {
		case CoverSoldOut:
			soldOut = append(soldOut, r.SKU)
		case CoverEstimated:
			estimated = append(estimated, r.SKU)
		case CoverFewOrders:
		}
	}
	if len(soldOut) != RankRows || more != 2 {
		t.Errorf("%d sold out rows listed and %d left off, want %d and 2", len(soldOut), more, RankRows)
	}
	if soldOut[0] != "OUT-11" {
		t.Errorf("first sold out row is %s, want OUT-11, the one that ran out last", soldOut[0])
	}
	if len(estimated) != RankRows || estimated[0] != "EST-00" {
		t.Errorf("estimated rows %v, want %d from EST-00: sold out rows must not take their places", estimated, RankRows)
	}
	for i, r := range listed[:RankRows] {
		if r.Estimate().State != CoverSoldOut {
			t.Errorf("row %d is %s, want the sold out group first", i, r.SKU)
		}
	}
}
