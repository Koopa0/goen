// Package warranty records which units a customer has claimed cover for.
//
// Cover is bounded by what was DELIVERED, never by what was dispatched: the days
// in transit come off the customer otherwise. The term is per product and may be
// absent, in which case nothing can be registered rather than a term invented.
package warranty

import (
	"errors"
	"fmt"
)

const MaxSerialRunes = 60

var (
	// ErrNotFound is one error for an order this customer does not own and one
	// that does not exist, because telling them apart is what a prober wants.
	ErrNotFound       = errors.New("warranty: no such order")
	ErrNotRegistrable = errors.New("warranty: this unit cannot be registered")
	ErrSerialTaken    = errors.New("warranty: that serial number is already registered")
	ErrInvalid        = errors.New("warranty: invalid registration")

	// ErrSerialTooLong also matches ErrInvalid, so callers that handle all
	// invalid registrations alike retain that behavior.
	ErrSerialTooLong = fmt.Errorf("%w: serial number is too long", ErrInvalid)
)
