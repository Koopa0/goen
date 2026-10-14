// Package refund names refunds.status, the closed set refunds_status_known
// admits, and the two outcomes the refund desk and the return desk both tell
// apart: a refund write the database refused, and an approved decision whose
// refund work is unfinished. The desks reach one another only through the
// consumer-defined Payouts interface, so the errors that interface promises
// live here, where neither desk has to import the other.
package refund

import "errors"

// Status is refunds.status.
type Status string

const (
	Pending        Status = "pending"
	RequiresAction Status = "requires_action"
	Succeeded      Status = "succeeded"
	Failed         Status = "failed"
	Cancelled      Status = "cancelled"
)

var (
	// ErrRefused is a refund write the database declined; its message is the
	// database's own, because that names the rule.
	ErrRefused = errors.New("refund: refused")
	// ErrIncomplete is an approved decision with an unfinished refund step: a
	// payout, its refunded event or points bookkeeping, or the order's
	// post-settlement cancellation. The decision stands; Resume continues the
	// first unfinished step.
	ErrIncomplete = errors.New("refund: the decision is approved and the refund did not complete")
	// ErrCreateRejected is a decision from Stripe's refund-CREATE endpoint that
	// no refund object was created; no provider object exists. It is deliberately
	// narrower than a *stripe.Error: the preliminary LIST uses the same error
	// type, and treating a failed LIST as a rejected refund would free captured
	// money for a second claim while the first outcome is still unknown.
	ErrCreateRejected = errors.New("refund: Stripe rejected refund creation")
)
