// Package loyalty is goen's points programme.
//
// # Why points are not a second currency
//
// Points convert to store credit at one published rate and are spendable
// nowhere else. A balance a customer could spend directly would be a second
// currency needing its own rounding, its own refund rules, its own
// never-negative guard and its own reconciliation — all of which store credit
// already has and has already been got right.
//
// # Why the ledger is a ledger
//
// Same reason store_credit_entries is one: a balance column is a number two
// concurrent writers each read and each overwrite, and nothing finds it until
// money is missing. The balance is derived and the guard locks the account
// before it reads.
package loyalty

import (
	"errors"
	"math"
	"time"
)

// PointsPerHundred is what a committed order earns.
//
// One point per NT$100 spent, which is the ratio most Taiwanese shops use and
// the one a customer can do in their head. Fractions are DROPPED rather than
// rounded: rounding up would let a NT$50 order earn a point, and a programme
// that pays out on the smallest possible purchase is one people game.
const PointsPerHundred = 1

// PointsPerCredit is the exchange rate, in points per NT$1 of store credit.
//
// Ten points to the dollar, so NT$1,000 spent earns 10 points which are worth
// NT$1 — a 0.1% return. Low on purpose: this is a demo, and a rate somebody
// might mistake for a real offer is a promise the shop has not made.
//
// It lives HERE and nowhere else. An exchange rate written in two places is two
// rates the day one of them changes.
const PointsPerCredit = 10

// Validity is how long an award lasts.
//
// A year from the order, per ENTRY. "Points earned in March expire next March"
// is what a customer can be told and what a shop can honour; expiring a whole
// balance punishes the customer who keeps shopping, which is the opposite of
// what the programme is for.
const Validity = 365 * 24 * time.Hour

// MinRedemption is the smallest redemption goen accepts.
//
// A hundred points, which is NT$10 of credit. Below that the transaction costs
// more attention than it returns, and a redemption of one point produces a
// ledger entry nobody wants to read.
const MinRedemption = 100

// The errors a caller branches on.
var (
	// ErrNotEnough is a redemption larger than the balance. The database
	// refuses it too — loyalty_never_negative, under a lock — and this is what
	// turns that into a sentence.
	ErrNotEnough = errors.New("loyalty: not enough points")
	// ErrTooSmall is a redemption under MinRedemption.
	ErrTooSmall = errors.New("loyalty: that is below the minimum redemption")
	// ErrNoAccount is a customer who has never held points or credit.
	ErrNoAccount = errors.New("loyalty: no account")
)

// PointsFor is what an order total earns.
//
// Integer arithmetic throughout, and the fraction is dropped: a float here
// would be a float touching money's neighbour, and rounding up would pay out on
// a NT$50 order.
func PointsFor(totalCents int64) int64 {
	if totalCents <= 0 {
		return 0
	}
	return totalCents / 10000 * PointsPerHundred
}

// CreditFor is what a number of points is worth, in cents.
func CreditFor(points int64) int64 {
	if points <= 0 {
		return 0
	}
	return points / PointsPerCredit * 100
}

// Redeemable is the largest redemption a balance allows.
//
// Rounded DOWN to a whole exchange: redeeming 105 points at ten to the dollar
// would take 105 and return NT$10, quietly keeping five. The remainder stays in
// the balance where the customer can see it.
func Redeemable(balance int64) int64 {
	if balance < MinRedemption {
		return 0
	}
	return balance / PointsPerCredit * PointsPerCredit
}

// MembershipWindow is how far back a customer's spend is counted for their
// 會員等級.
//
// A ROLLING year, not lifetime. Lifetime tiers only ever go up, which turns a
// benefit into a permanent liability the shop cannot price — and a customer who
// spent NT$150,000 once in 2019 is not the customer the top tier is for.
//
// A year rather than a quarter because 3C is a considered purchase: somebody
// buying a laptop and then a monitor six months later is one relationship, and
// a window short enough to break that in half would read as arbitrary.
const MembershipWindow = 365 * 24 * time.Hour

// Days is a duration in whole days, which is the unit the SQL side takes.
//
// make_interval(days => n) rather than an interval in seconds, because the
// window is compared against placed_at across daylight-saving boundaries and
// "a year ago" is a calendar answer rather than an arithmetic one.
func Days(d time.Duration) int32 {
	// A duration is int64 nanoseconds and caps at ~292 years, so the day count
	// cannot leave int32's range — but the conversion is written to say so
	// rather than to be trusted.
	days := int64(d / (24 * time.Hour))
	//nolint:gosec // G115: clamped to [0, MaxInt32] on the line above, and a
	// time.Duration caps at ~292 years so the upper bound is unreachable.
	return int32(min(max(days, 0), math.MaxInt32))
}
