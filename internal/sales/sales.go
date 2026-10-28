// Package sales is what the shop sold over a run of shop days, as the reports
// desk reads it for another desk to show: each period's paid orders and
// revenue, day by day, the best sellers, and the newest paid order, and how a figure of one
// period is set against the period before it. The desks reach one another
// only through interfaces the consumer declares, so the values and the error
// those interfaces promise, and the comparison the report and the dashboard
// both write, live here, where no desk has to import another. It does not hold
// the work that waits for a person (admin/worklist) or the report that reads
// the figures (admin/reports).
package sales

import (
	"errors"
	"time"
)

// ErrLatestPaid is returned with a [Recent] whose latest paid order could not
// be read: the periods are whole and Latest is nil.
var ErrLatestPaid = errors.New("read latest paid order")

// Recent is the last run of shop days, the run of as many days before it, cut
// at the same time of day, and the newest paid order.
type Recent struct {
	Current, Previous Period
	// Days has a row for each of the last twice as many shop days, a day
	// without orders included. Every day is whole but today, which ends at the
	// time the run is read: unlike the two periods, which meet at the same time
	// of day, no hours fall between its days.
	Days []Day
	// Sellers are the best sellers of Current, at most [RecentSellers] of them.
	Sellers []Seller
	// Latest is nil when no order was ever paid.
	Latest *PaidOrder
}

// Period is a run of shop days: its paid orders and revenue, and each day's.
type Period struct {
	Orders       int64
	RevenueCents int64
	// RevenueSquares is the sum of the squared order totals, which the noise
	// of RevenueCents is read from.
	RevenueSquares float64
	// Days has a row for every shop day of the period, a day without orders
	// included.
	Days []Day
	// Partial is a period whose last shop day is cut short.
	Partial bool
}

// Day is one shop day's paid orders and their revenue.
type Day struct {
	Day          time.Time
	Orders       int64
	RevenueCents int64
}

// PaidOrder is an order whose money has come in, and how long ago.
type PaidOrder struct {
	Number     string
	TotalCents int64
	Elapsed    time.Duration
}
