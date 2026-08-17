package outbox_test

import (
	"testing"
	"time"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/outbox"
)

// TestTheLeaseCoversTheWholeBatch: one lease covers the whole batch and the
// batch is delivered SERIALLY, so work exceeding the lease leaves the tail
// visible to every other replica while this one is still sending it.
func TestTheLeaseCoversTheWholeBatch(t *testing.T) {
	t.Parallel()

	work := time.Duration(outbox.BatchSize) * outbox.HandlerBudget
	if work+outbox.LeaseMargin > outbox.Lease {
		t.Errorf("a full batch can take %v and the lease is %v (with %v held back "+
			"for everything that is not a handler) — the claim expires while the "+
			"worker is still delivering it, and a second replica sends the rest again",
			work, outbox.Lease, outbox.LeaseMargin)
	}
}

// TestTheHandlerBudgetIsTheSendersOwnTimeout is the drift half of the same lock:
// [outbox.HandlerBudget] mirrors email.SendTimeout, and raising the sender's
// timeout would otherwise quadruple a batch's work under an unchanged lease.
func TestTheHandlerBudgetIsTheSendersOwnTimeout(t *testing.T) {
	t.Parallel()

	if outbox.HandlerBudget < email.SendTimeout {
		t.Errorf("HandlerBudget is %v and email.SendTimeout is %v — the lease "+
			"arithmetic is sized for a handler that can no longer finish inside it",
			outbox.HandlerBudget, email.SendTimeout)
	}
}
