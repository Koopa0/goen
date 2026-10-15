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

// UnsentReason is why a paid unit of a partly shipped order will not be sent:
// the code return_requests_unsent_reason_known admits on an unsent refund.
type UnsentReason string

// Damaged, lost and supplier-failed units do not exist any more, so
// settle_unsent_refund writes them off; a unit the customer withdrew goes
// back on sale.
const (
	UnsentDamaged          UnsentReason = "damaged"
	UnsentLost             UnsentReason = "lost"
	UnsentSupplierFailed   UnsentReason = "supplier_failed"
	UnsentCustomerWithdrew UnsentReason = "customer_withdrew"
)

// UnsentReasons lists every UnsentReason in the order the form offers them.
var UnsentReasons = [...]UnsentReason{
	UnsentDamaged,
	UnsentLost,
	UnsentSupplierFailed,
	UnsentCustomerWithdrew,
}

// ParseUnsentReason reads the form's code. Anything else is refused here, so a
// crafted form meets a notice rather than the database's CHECK.
func ParseUnsentReason(s string) (UnsentReason, bool) {
	r := UnsentReason(s)
	switch r {
	case UnsentDamaged, UnsentLost, UnsentSupplierFailed, UnsentCustomerWithdrew:
		return r, true
	default:
		return "", false
	}
}
