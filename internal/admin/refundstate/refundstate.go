// Package refundstate names refunds.status, the closed set refunds_status_known
// admits, and the two outcomes of a refund its callers tell apart: refused, and
// approved but not paid.
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
	// ErrIncomplete is a return that WAS approved and whose money did not
	// go: the decision stands and cannot be retaken, and the refund claim has
	// already committed a `pending` row keyed on the return.
	ErrIncomplete = errors.New("refundstate: the return is approved and the refund did not complete")
)
