// Package returns is the customer's side of sending something back. Deciding
// one and refunding it are back-office writes and live in internal/admin.
//
// What may be returned is bounded by what SHIPPED, a rule the database holds in
// return_within_shipment.
package returns

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/i18n"
)

// Sentinel errors, each a distinct decision for the handler.
var (
	// ErrNotFound is an order number that names nothing.
	ErrNotFound = errors.New("returns: order not found")
	// ErrNotReturnable is an order nothing can be sent back from — nothing has
	// shipped, or every shipped unit is already claimed.
	ErrNotReturnable = errors.New("returns: nothing on this order can be returned")
	// ErrAlreadyOpen is a second request while one is still undecided.
	ErrAlreadyOpen = errors.New("returns: this order already has an open request")
	// ErrInvalid is a form goen refused before the database saw it.
	ErrInvalid = errors.New("returns: invalid request")
)

// MaxReasonRunes bounds the reason field.
const MaxReasonRunes = 500

// Line is one order line a customer may send back.
type Line struct {
	ID         string
	SKU        string
	Name       string
	Label      string
	UnitCents  int64
	Returnable int32
}

// Request is what a customer is asking to send back.
type Request struct {
	Reason string
	// Lines maps an order line id to how many units of it.
	Lines map[string]int32
}

// Validate refuses what the form should never have submitted. A blank reason is
// legal: Consumer Protection Act §19 I lets a consumer rescind inside seven days
// without giving one, and §19 V voids any agreement otherwise.
func (r *Request) Validate() error {
	r.Reason = strings.TrimSpace(r.Reason)
	if utf8.RuneCountInString(r.Reason) > MaxReasonRunes {
		return ErrInvalid
	}
	for _, c := range r.Reason {
		if unicode.IsControl(c) && c != '\n' && c != '\t' && c != '\r' {
			return ErrInvalid
		}
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

// StatusLabel is a return's state in the chrome language.
func StatusLabel(ctx context.Context, s string) string {
	switch s {
	case "requested":
		return i18n.T(ctx, i18n.KeyReturnStateOpen)
	case "approved":
		return i18n.T(ctx, i18n.KeyReturnStateApproved)
	case "rejected":
		return i18n.T(ctx, i18n.KeyReturnStateRefused)
	case "completed":
		return i18n.T(ctx, i18n.KeyReturnStateDone)
	default:
		panic("returns: no label for status " + s)
	}
}
