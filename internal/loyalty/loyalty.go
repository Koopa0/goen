// Package loyalty is goen's points programme.
//
// Points convert to store credit at one published rate and are spendable nowhere
// else. The balance is derived from a ledger rather than stored, and the guard
// locks the account before it reads.
package loyalty

import (
	"errors"
	"time"
)

// PointsPerCredit is the exchange rate, in points per NT$1 of store credit.
const PointsPerCredit = 10

// MinRedemption is the smallest redemption goen accepts.
const MinRedemption = 100

// MaxRedemptionPoints is the largest redemption the economic ledger accepts.
// Keep it in step with redeem_loyalty_points: a bound below the database's own
// would refuse the replay of an operation the database could have completed.
const MaxRedemptionPoints int64 = 1_000_000_000

var (
	// ErrNotEnough is a redemption larger than the balance.
	ErrNotEnough = errors.New("loyalty: not enough points")
	// ErrTooSmall is a redemption under MinRedemption.
	ErrTooSmall = errors.New("loyalty: that is below the minimum redemption")
	// ErrNoAccount is a customer who has never held points or credit.
	ErrNoAccount = errors.New("loyalty: no account")
)

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
