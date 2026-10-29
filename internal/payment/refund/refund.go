// Package refund names refunds.status, the closed set refunds_status_known
// admits; the two outcomes the refund desk and the return desk both tell
// apart, a refund write the database refused and an approved decision whose
// refund work is unfinished; what the refund desk reports to the other
// desks, the refunds an order page offers ([Offer]) and where a return's
// payout stands ([Payout]); and the words both desks name a payout's card and
// store-credit halves in ([PayoutChannel]). The desks reach one another only
// through interfaces the consumer declares, so the values and errors those
// interfaces promise, and the words both write, live here, where no desk has
// to import another.
package refund

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

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
	// ErrFollowUpIncomplete is an ErrIncomplete whose money has already gone
	// back: every source settled and only the refunded event or the points
	// clawback is missing. Resume finishes that step. A desk that tells the
	// two apart tests this first, because a notice that sends staff to the
	// provider for a refund that is not missing invites a second one.
	ErrFollowUpIncomplete = fmt.Errorf("%w: the money went back and a closing step did not complete", ErrIncomplete)
	// ErrCreateRejected is a decision from Stripe's refund-CREATE endpoint that
	// no refund object was created; no provider object exists. It is deliberately
	// narrower than a *stripe.Error: the preliminary LIST uses the same error
	// type, and treating a failed LIST as a rejected refund would free captured
	// money for a second claim while the first outcome is still unknown.
	ErrCreateRejected = errors.New("refund: Stripe rejected refund creation")
)

// Offer is what an order page offers of the two refunds staff run from it: the
// refund before shipment, which pays a paid order back in full while nothing
// has shipped, and the refund of the units a partly shipped order will not
// send.
type Offer struct {
	// Offered is a paid order nothing has shipped from and no return exists
	// for: the page offers the refund before shipment.
	Offered bool
	// Open is a refund before shipment that began and has not finished;
	// pressing it again resumes it.
	Open bool
	// CreditPaid is an offered one store credit alone paid, which the refund
	// cancels at once.
	CreditPaid bool
	// Opened reports whether a refund before shipment was ever opened: an
	// order being refunded is neither picked nor shipped.
	Opened bool

	// UnsentOffered is an order that has sent a parcel and still owes units:
	// the page offers the refund of those it will not send.
	UnsentOffered bool
	// UnsentOpen is a refund of unsent units that Resume finishes;
	// UnsentUnits are the units it covers.
	UnsentOpen  bool
	UnsentUnits []UnsentUnit
}

// UnsentUnit is one line of a refund of units an order will not send.
type UnsentUnit struct {
	LineID      uuid.UUID
	Name, Label string
	Quantity    int32
}

// Payout is where a return's refund stands, as the return queue shows it.
type Payout struct {
	// CardCents and CreditCents are the source split the approval froze. Both
	// stay zero until approval freezes them.
	CardCents, CreditCents int64
	// Owed is an approved return whose money, refunded event or points
	// bookkeeping is still outstanding.
	Owed bool
	// MoneySettled is a payout whose every source has gone back. With Owed it
	// means only the refunded event or the points clawback is left.
	MoneySettled bool
	// Unfit is non-nil when the durable payout facts no longer fit the frozen
	// split: the return a person must repair before a retry is safe. It wraps
	// ErrRefused, and Owed is then true.
	Unfit error
}
