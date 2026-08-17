// Package warranty records which units a customer has claimed cover for.
//
// A registration is bounded by what was DELIVERED, never by what was dispatched:
// counting from dispatch takes one to three days off the customer's cover. The
// term is per product and may be absent, in which case nothing can be registered
// — defaulting it would be goen inventing a promise nobody made.
package warranty

import "errors"

// MaxSerialRunes bounds a serial number.
const MaxSerialRunes = 60

// The errors a caller branches on.
var (
	// ErrNotFound is an order this customer does not own, or does not exist. One
	// error for both, because telling them apart is what a prober wants.
	ErrNotFound = errors.New("warranty: no such order")
	// ErrNotRegistrable is a unit that cannot be registered: not delivered, no
	// term set, already registered, or beyond what was bought.
	ErrNotRegistrable = errors.New("warranty: this unit cannot be registered")
	// ErrSerialTaken is a serial number already registered.
	ErrSerialTaken = errors.New("warranty: that serial number is already registered")
	// ErrInvalid is a malformed submission.
	ErrInvalid = errors.New("warranty: invalid registration")
)
