// Package refund names refunds.status, the closed set refunds_status_known
// admits; the two outcomes the refund desk and the return desk both tell
// apart, a refund write the database refused and an approved decision whose
// refund work is unfinished; and what the refund desk reports to the other
// desks, the refunds an order page offers ([Offer]) and where a return's
// payout stands ([Payout]). The desks reach one another only through
// interfaces the consumer declares, so the values and errors those interfaces
// promise live here, where no desk has to import another.
package refund

import (
	"errors"

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
	// Unfit is non-nil when the durable payout facts no longer fit the frozen
	// split: the return a person must repair before a retry is safe. It wraps
	// ErrRefused, and Owed is then true.
	Unfit error
}
