package inventory

import (
	"cmp"
	"math"
	"slices"
	"time"
)

const (
	// CoverWindowDays is the fewest shop days of sales a rate is read from: a
	// week is too short to tell a slow SKU from an idle one.
	CoverWindowDays = 30

	// CoverMinOrders is the fewest orders that bought a SKU before its days
	// cover is estimated. Orders, not units: one order of ten units is one
	// event, and the interval is a count of events.
	CoverMinOrders = 10

	// RankRows is how many rows each of the two groups lists, sold out and
	// the rest, so neither can push the other off the page; the sold out ones
	// left off are counted.
	RankRows = 10

	// CoverWarnDays is the days cover under which a SKU is marked: the
	// restocking lead, not the period the report shows.
	CoverWarnDays = 30

	// CoverMaxDays is where the scale and every estimate end.
	CoverMaxDays = 90

	// z90 is the normal quantile bounding a 90% interval on each side.
	z90 = 1.6448536269514722
)

// Sales is a SKU's stock and what was sold from it over the window.
type Sales struct {
	Sellable int32
	// Sold is units; Orders is how many orders bought it.
	Sold   int64
	Orders int64
	// InStock is how long over the window the SKU had anything to sell.
	InStock time.Duration
}

// CoverState says what can be said of a SKU's days cover.
type CoverState int

const (
	CoverSoldOut CoverState = iota
	CoverEstimated
	CoverFewOrders
)

// Cover is how many days a SKU's sellable stock lasts at its rate of sale,
// with the 90% range that rate allows. Days, Low and High are whole days and
// are set only when State is CoverEstimated.
type Cover struct {
	State           CoverState
	Days, Low, High int
}

// Estimate reads the SKU's days cover. A SKU with nothing sellable is sold out
// whatever it sold; the rate is units over the days it was in stock, and the
// range is the Poisson interval of the orders that made it.
func (s Sales) Estimate() Cover {
	switch {
	case s.Sellable <= 0:
		return Cover{State: CoverSoldOut}
	case s.Orders < CoverMinOrders:
		return Cover{State: CoverFewOrders}
	}
	// A SKU received minutes ago has few hours in stock; the floor keeps the
	// rate finite, and the estimate of such a SKU is one day.
	perDay := float64(s.Sold) / (max(s.InStock, time.Hour).Hours() / 24)
	days := float64(s.Sellable) / perDay
	k := float64(s.Orders)
	slowest := chiSquareQuantile(-z90, 2*k) / (2 * k)
	fastest := chiSquareQuantile(z90, 2*k+2) / (2 * k)
	// The interval is the orders' Poisson one, applied to a rate in units: it
	// takes the units per order as fixed, so with orders of varying size the
	// true spread is wider.
	return Cover{
		State: CoverEstimated,
		Days:  wholeDays(days),
		Low:   wholeDays(days / fastest),
		High:  wholeDays(days / slowest),
	}
}

// wholeDays rounds to the nearest day, and never to none: stock that lasts
// hours is a day.
func wholeDays(d float64) int { return max(int(math.Round(d)), 1) }

// chiSquareQuantile is the Wilson–Hilferty approximation of the chi-square
// quantile with dof degrees of freedom at normal quantile z.
func chiSquareQuantile(z, dof float64) float64 {
	a := 2 / (9 * dof)
	c := 1 - a + z*math.Sqrt(a)
	return dof * c * c * c
}

// Urgent reports whether the row is one to act on: sold out, or an estimate
// under the warning line. A sold out row shows its badge and an estimated one
// the ▲.
func (c Cover) Urgent() bool {
	return c.State == CoverSoldOut || c.State == CoverEstimated && c.Days < CoverWarnDays
}

// Beyond reports whether the estimate is past the end of the scale.
func (c Cover) Beyond() bool { return c.Days > CoverMaxDays }

// Risk is one SKU's sales over the window, with what names it.
type Risk struct {
	Sales

	SKU  string
	Name string
	Slug string
	// SoldOutAt is when the SKU last ran out within the window; zero when it
	// was already out at its start.
	SoldOutAt time.Time
}

// Rank lists the sold out SKUs that ran out most recently, then the
// estimated ones from the shortest days cover, then those that cannot be
// estimated, each of the two groups at most RankRows. It also returns how
// many sold out SKUs it left off.
func Rank(rows []Risk) (listed []Risk, moreSoldOut int) {
	var soldOut, rest []Risk
	for i := range rows {
		if rows[i].Estimate().State == CoverSoldOut {
			soldOut = append(soldOut, rows[i])
		} else {
			rest = append(rest, rows[i])
		}
	}
	slices.SortStableFunc(soldOut, func(a, b Risk) int {
		return cmp.Or(b.SoldOutAt.Compare(a.SoldOutAt), cmp.Compare(b.Sold, a.Sold), cmp.Compare(a.SKU, b.SKU))
	})
	slices.SortStableFunc(rest, func(a, b Risk) int {
		ea, eb := a.Estimate(), b.Estimate()
		return cmp.Or(
			cmp.Compare(ea.State, eb.State),
			cmp.Compare(ea.Days, eb.Days),
			cmp.Compare(a.Sellable, b.Sellable),
			cmp.Compare(b.Sold, a.Sold),
			cmp.Compare(a.SKU, b.SKU),
		)
	})
	shown := min(len(soldOut), RankRows)
	listed = make([]Risk, 0, shown+min(len(rest), RankRows))
	listed = append(listed, soldOut[:shown]...)
	listed = append(listed, rest[:min(len(rest), RankRows)]...)
	return listed, len(soldOut) - shown
}
