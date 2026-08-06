// Package warranty records which units a customer has claimed cover for.
//
// goen sells 3C on the promise that the warranty is dependable, and this is the
// half of that promise the software can keep: a customer says "this is the one
// I bought", and the shop can answer "yes, until this date" without either side
// looking for a receipt.
//
// # What bounds a registration
//
// What SHIPPED, never what was ordered. A warranty starts when goods reach
// somebody, so registering cover for a box still in the warehouse would start
// the clock early — the same rule internal/returns follows, for the same
// reason.
//
// The term is per PRODUCT and may be absent. A phone and a braided cable do not
// carry the same cover, and a product whose term nobody set cannot be
// registered at all: a warranty of unstated length is a promise nobody made,
// and defaulting it would be goen inventing one.
package warranty

import "errors"

// MaxSerialRunes bounds a serial number.
//
// Sixty. Long enough for any manufacturer's format, short enough that the field
// is not a place to paste an essay.
const MaxSerialRunes = 60

// The errors a caller branches on.
var (
	// ErrNotFound is an order this customer does not own, or does not exist.
	// One error for both, because the difference is exactly what a caller
	// probing order numbers wants to learn.
	ErrNotFound = errors.New("warranty: no such order")
	// ErrNotRegistrable is a unit that cannot be registered: not shipped, no
	// term set, already registered, or beyond what was bought.
	ErrNotRegistrable = errors.New("warranty: this unit cannot be registered")
	// ErrSerialTaken is a serial number already registered, which usually means
	// somebody typed the wrong one rather than that two products share it.
	ErrSerialTaken = errors.New("warranty: that serial number is already registered")
	// ErrInvalid is a malformed submission.
	ErrInvalid = errors.New("warranty: invalid registration")
)
