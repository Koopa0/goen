// Package returns decides a return request: the §19 window each line falls in,
// what the buyer is owed, why a decision is refused, and the request's status.
package returns

type Status string

// The four states return_requests_refund_snapshot_shape allows.
const (
	StatusRequested Status = "requested"
	StatusApproved  Status = "approved"
	StatusRejected  Status = "rejected"
	StatusCompleted Status = "completed"
)

// Statuses lists every Status, for what must cover all of them.
var Statuses = [...]Status{
	StatusRequested,
	StatusApproved,
	StatusRejected,
	StatusCompleted,
}
