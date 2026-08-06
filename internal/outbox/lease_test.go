package outbox_test

import (
	"testing"
	"time"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/outbox"
)

// TestTheLeaseCoversTheWholeBatch holds the arithmetic that makes a claim
// exclusive.
//
// A claim takes ONE lease over the whole batch — the UPDATE pushes available_at
// forward for every row at once — and the batch is then delivered SERIALLY.
// So the last message in a batch starts (BatchSize-1) handler budgets after the
// claim and finishes one more later, and if that exceeds the lease it is no
// longer claimed by the time it is being sent: a second replica's
// `available_at <= now()` matches it, and two workers deliver it.
//
// The numbers used to be fifty messages at thirty seconds each under a
// five-minute lease. Ten of the fifty were covered. The other forty were
// visible to every other replica while this one was still working through them,
// which for a receipt is a duplicate and for a password reset is a second live
// token in somebody's mailbox.
//
// Three independent constants, so this is a relationship and not a restatement:
// change any one of them and the test says whether the other two still hold.
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

// TestTheHandlerBudgetIsTheSendersOwnTimeout is the drift half of the same
// lock.
//
// [outbox.HandlerBudget] is a mirror of email.SendTimeout, because the queue has
// no business importing the mail for one number. A mirror with nothing checking
// it is how the arithmetic above goes quietly wrong: raise the sender's timeout
// to two minutes and every batch becomes four times the work under the same
// lease, with nothing anywhere to say so.
//
// The mail sender is the slowest handler goen registers, and it is the one whose
// timeout is a stated bound rather than a hope.
func TestTheHandlerBudgetIsTheSendersOwnTimeout(t *testing.T) {
	t.Parallel()

	if outbox.HandlerBudget < email.SendTimeout {
		t.Errorf("HandlerBudget is %v and email.SendTimeout is %v — the lease "+
			"arithmetic is sized for a handler that can no longer finish inside it",
			outbox.HandlerBudget, email.SendTimeout)
	}
}
