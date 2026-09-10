package outbox_test

import (
	"testing"
	"time"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/outbox"
)

// TestTheLeaseCoversTheWholeBatch: one lease covers a batch that is delivered
// SERIALLY.
func TestTheLeaseCoversTheWholeBatch(t *testing.T) {
	t.Parallel()

	// Each message costs its handler AND the write that records the outcome.
	// Counting only the handler leaves the settle write on whatever the pool's
	// statement_timeout happens to be, which is set for a storefront request by
	// a different file for a different reason.
	perMessage := outbox.HandlerBudget + outbox.SettleBudget
	work := time.Duration(outbox.BatchSize) * perMessage
	if work+outbox.LeaseMargin > outbox.Lease {
		t.Errorf("a full batch can take %v (%d × %v) and the lease is %v with %v "+
			"held back for the claim and the clocks — the claim expires while the "+
			"worker is still delivering it, and a second replica sends the rest again",
			work, outbox.BatchSize, perMessage, outbox.Lease, outbox.LeaseMargin)
	}
}

// TestTheHandlerBudgetIsTheSendersOwnTimeout: [outbox.HandlerBudget] mirrors
// email.SendTimeout, which the lease arithmetic above is sized for.
func TestTheHandlerBudgetIsTheSendersOwnTimeout(t *testing.T) {
	t.Parallel()

	if outbox.HandlerBudget < email.SendTimeout {
		t.Errorf("HandlerBudget is %v and email.SendTimeout is %v — the lease "+
			"arithmetic is sized for a handler that can no longer finish inside it",
			outbox.HandlerBudget, email.SendTimeout)
	}
}
