// Package sales is what the shop sold over a run of shop days, as the reports
// desk reads it for another desk to show: each period's paid orders and
// revenue, every shop day of a run, its best sellers and the newest paid
// order, and how a figure of one period is set against the period before it.
// The desks reach one another only through interfaces the consumer declares,
// so the values and the error those interfaces promise, and the comparison the
// report and the dashboard both write, live here, where no desk has to import
// another. It does not hold the work that waits for a person (admin/worklist) or the
// report that reads the figures (admin/reports).
package sales

import (
	"errors"
	"time"
)

// ErrLatestPaid is returned with a [Recent] whose latest paid order could not
// be read: the periods are whole and Latest is nil.
var ErrLatestPaid = errors.New("read latest paid order")

// Recent is the last run of shop days and the run of as many days before it,
// cut at the same time of day; every shop day of both, whole; the current
// run's best sellers; and the newest paid order.
type Recent struct {
	Current, Previous Period
	// Days has a row for every shop day from Previous's first to today, a day
	// without orders included. Each day is whole but today, which ends at the
	// time the run is read. The two periods meet at the same time of day, so
	// the hours of Previous's last day after that time are in Days and in
	// neither period.
	Days []Day
	// Sellers are the best sellers of Current, at most [RecentSellers] of them.
	Sellers []Seller
	// Latest is nil when no order was ever paid.
	Latest *PaidOrder
}

// Period is a run of shop days: its paid orders and revenue.
type Period struct {
	Orders       int64
	RevenueCents int64
	// RevenueSquares is the sum of the squared order totals, which the noise
	// of RevenueCents is read from.
	RevenueSquares float64
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
