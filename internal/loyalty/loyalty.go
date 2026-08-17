// Package loyalty is goen's points programme.
//
// Points convert to store credit at one published rate and are spendable nowhere
// else. The balance is derived from a ledger rather than stored, and the guard
// locks the account before it reads.
package loyalty

import (
	"errors"
	"math"
	"time"
)

// PointsPerHundred is what a committed order earns per NT$100 spent.
const PointsPerHundred = 1

// PointsPerCredit is the exchange rate, in points per NT$1 of store credit.
const PointsPerCredit = 10

// Validity is how long an award lasts, per entry rather than per balance.
const Validity = 365 * 24 * time.Hour

// MinRedemption is the smallest redemption goen accepts.
const MinRedemption = 100

// The errors a caller branches on.
var (
	// ErrNotEnough is a redemption larger than the balance.
	ErrNotEnough = errors.New("loyalty: not enough points")
	// ErrTooSmall is a redemption under MinRedemption.
	ErrTooSmall = errors.New("loyalty: that is below the minimum redemption")
	// ErrNoAccount is a customer who has never held points or credit.
	ErrNoAccount = errors.New("loyalty: no account")
)

// PointsFor is what an order total earns; the fraction is dropped, never
// rounded, or a NT$50 order pays out.
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

// Redeemable is the largest redemption a balance allows, rounded down to a whole
// exchange so no remainder is quietly kept.
func Redeemable(balance int64) int64 {
	if balance < MinRedemption {
		return 0
	}
	return balance / PointsPerCredit * PointsPerCredit
}

// MembershipWindow is how far back a customer's spend is counted for their
// membership tier. A rolling year: a lifetime tier is a permanent liability.
const MembershipWindow = 365 * 24 * time.Hour

// Days is a duration in whole days, which is the unit the SQL side takes. It
// feeds make_interval(days => n), so "a year ago" stays a calendar answer across
// daylight-saving boundaries.
func Days(d time.Duration) int32 {
	days := int64(d / (24 * time.Hour))
	//nolint:gosec // G115: clamped to [0, MaxInt32] on the line above, and a
	// time.Duration caps at ~292 years so the upper bound is unreachable.
	return int32(min(max(days, 0), math.MaxInt32))
}
