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

const PointsPerCredit = 10

const MinRedemption = 100

// MaxRedemptionPoints must stay in step with redeem_loyalty_points: a bound
// below the database's own would refuse the replay of an operation the database
// could have completed.
const MaxRedemptionPoints int64 = 1_000_000_000

var (
	ErrNotEnough = errors.New("loyalty: not enough points")
	// ErrReturnUnsettled is a redemption while an approved return has yet to
	// pay out, and so has yet to claw back its points.
	ErrReturnUnsettled = errors.New("loyalty: an approved return is not paid out yet")
	ErrTooSmall        = errors.New("loyalty: that is below the minimum redemption")
	ErrNoAccount       = errors.New("loyalty: no account")
	// ErrInvalidOperation is separate from ErrNoAccount: folding it in tells a
	// funded account they have no points.
	ErrInvalidOperation = errors.New("loyalty: invalid operation")
)

func CreditFor(points int64) int64 {
	if points <= 0 {
		return 0
	}
	return points / PointsPerCredit * 100
}

// Redeemable rounds down to a whole exchange so no remainder is quietly kept.
func Redeemable(balance int64) int64 {
	if balance < MinRedemption {
		return 0
	}
	return balance / PointsPerCredit * PointsPerCredit
}

// MembershipWindow is a rolling year: a lifetime tier is a permanent liability.
const MembershipWindow = 365 * 24 * time.Hour
