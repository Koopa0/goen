// Package sales is what the shop sold over a run of shop days, as the reports
// desk reads it for another desk to show: each period's paid orders and
// revenue, day by day, and the newest paid order. The desks reach one another
// only through interfaces the consumer declares, so the values and the error
// those interfaces promise live here, where no desk has to import another.
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
