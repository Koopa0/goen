// Package buyer is the buyer's side of a placed order: finding it by number and
// email address, /orders/find; its page, /orders/{number}; cancelling it while
// it is pending, /orders/{number}/cancel; and the form for sending something
// back, /orders/{number}/return. What may be returned is bounded by what
// SHIPPED, a rule the database holds in return_within_shipment.
package buyer

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrNotFound      = errors.New("buyer: order not found")
	ErrNotReturnable = errors.New("buyer: nothing on this order can be returned")
	ErrReturnOpen    = errors.New("buyer: this order already has an open request")
	ErrReturnInvalid = errors.New("buyer: invalid request")
	ErrReturnTooMany = errors.New("buyer: quantity exceeds returnable amount")
	// ErrReturnAccountErased means a concurrent erasure removed the only destination
	// for the return's store-credit payout before the request could commit.
	ErrReturnAccountErased = errors.New("buyer: account was erased before the return committed")
)

const MaxReturnReasonRunes = 500

type ReturnLine struct {
	ID         string
	SKU        string
	Name       string
	Label      string
	UnitCents  int64
	Returnable int32
}

type ReturnRequest struct {
	Reason string
	Lines  map[string]int32
}

// Validate allows a blank reason: Consumer Protection Act §19 I lets a consumer
// rescind inside seven days without giving one, and §19 V voids any agreement
// otherwise.
func (r *ReturnRequest) Validate() error {
	r.Reason = strings.TrimSpace(r.Reason)
	if !validReturnReason(r.Reason) {
		return ErrReturnInvalid
	}

	total := int32(0)
	for _, q := range r.Lines {
		if q < 0 {
			return ErrReturnInvalid
		}
		total += q
	}
	if total == 0 {
		return ErrReturnInvalid
	}
	return nil
}

func validReturnReason(reason string) bool {
	return validateReturnReason(reason) == reasonValid
}

type reasonValidation string

const (
	reasonValid               reasonValidation = ""
	reasonTooLong             reasonValidation = "too long"
	reasonUnsupportedControls reasonValidation = "unsupported controls"
)

func validateReturnReason(reason string) reasonValidation {
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > MaxReturnReasonRunes {
		return reasonTooLong
	}
	if strings.ContainsFunc(reason, func(c rune) bool {
		return unicode.IsControl(c) && c != '\n' && c != '\t' && c != '\r'
	}) {
		return reasonUnsupportedControls
	}
	return reasonValid
}
