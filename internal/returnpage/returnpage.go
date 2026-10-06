// Package returnpage is the buyer's form for sending something back,
// /orders/{number}/return. What may be returned is bounded by what SHIPPED, a
// rule the database holds in return_within_shipment.
package returnpage

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrNotFound      = errors.New("returnpage: order not found")
	ErrNotReturnable = errors.New("returnpage: nothing on this order can be returned")
	ErrAlreadyOpen   = errors.New("returnpage: this order already has an open request")
	ErrInvalid       = errors.New("returnpage: invalid request")
	ErrTooMany       = errors.New("returnpage: quantity exceeds returnable amount")
	// ErrAccountErased means a concurrent erasure removed the only destination
	// for the return's store-credit payout before the request could commit.
	ErrAccountErased = errors.New("returnpage: account was erased before the return committed")
)

const MaxReasonRunes = 500

type Line struct {
	ID         string
	SKU        string
	Name       string
	Label      string
	UnitCents  int64
	Returnable int32
}

type Request struct {
	Reason string
	Lines  map[string]int32
}

// Validate allows a blank reason: Consumer Protection Act §19 I lets a consumer
// rescind inside seven days without giving one, and §19 V voids any agreement
// otherwise.
func (r *Request) Validate() error {
	r.Reason = strings.TrimSpace(r.Reason)
	if !validReturnReason(r.Reason) {
		return ErrInvalid
	}

	total := int32(0)
	for _, q := range r.Lines {
		if q < 0 {
			return ErrInvalid
		}
		total += q
	}
	if total == 0 {
		return ErrInvalid
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
	if utf8.RuneCountInString(reason) > MaxReasonRunes {
		return reasonTooLong
	}
	if strings.ContainsFunc(reason, func(c rune) bool {
		return unicode.IsControl(c) && c != '\n' && c != '\t' && c != '\r'
	}) {
		return reasonUnsupportedControls
	}
	return reasonValid
}
