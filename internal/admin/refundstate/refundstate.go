// Package refundstate names refunds.status, the closed set refunds_status_known
// admits: the refund writer records it and the health page reads it.
package refundstate

type State string

const (
	Pending        State = "pending"
	RequiresAction State = "requires_action"
	Succeeded      State = "succeeded"
	Failed         State = "failed"
	Cancelled      State = "cancelled"
)
