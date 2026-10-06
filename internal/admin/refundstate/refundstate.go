// Package refundstate names refunds.status, the closed set refunds_status_known
// admits, and the outcomes its callers tell apart: refused, or approved with
// unfinished refund work.
package refundstate

import "errors"

type State string

const (
	Pending        State = "pending"
	RequiresAction State = "requires_action"
	Succeeded      State = "succeeded"
	Failed         State = "failed"
	Cancelled      State = "cancelled"
)

var (
	// ErrRefused is a refund write the database declined; its message is the
	// database's own, because that names the rule.
	ErrRefused = errors.New("refundstate: refused")
	// ErrIncomplete is an approved return with an unfinished refund step: a
	// payout, its refunded event or points bookkeeping, or the order's
	// post-settlement cancellation. The decision stands; Resume continues the
	// first unfinished step.
	ErrIncomplete = errors.New("refundstate: the return is approved and the refund did not complete")
)
