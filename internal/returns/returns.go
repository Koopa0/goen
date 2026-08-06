// Package returns is the customer's side of sending something back.
//
// The decision and the money are NOT here: approving a return and issuing a
// refund are back-office writes on the admin pool, and they live in
// internal/admin. This package runs as `store`, which is what a customer-facing
// request may do — it can open a request and read its own order's, and nothing
// it can reach touches a payment.
//
// What a customer may return is bounded by what SHIPPED, not by what was
// ordered. The database holds that rule in return_within_shipment; the form
// here shows the same number so the page offers what the write will accept.
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
//
// Counted in RUNES, not bytes. A Traditional Chinese reason is three bytes a
// character, so a byte limit would cut a Chinese customer off at a third of the
// length an English one gets — and could split a character in half.
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

// Validate refuses what the form should never have submitted.
//
// The database refuses these too. Checking here turns a constraint violation —
// which reaches the customer as a 500 — into a message on the form with their
// own words still in it.
func (r *Request) Validate() error {
	r.Reason = strings.TrimSpace(r.Reason)
	if r.Reason == "" {
		return ErrInvalid
	}
	if utf8.RuneCountInString(r.Reason) > MaxReasonRunes {
		return ErrInvalid
	}
	for _, c := range r.Reason {
		// unicode.IsControl covers C0, DEL and C1. Tab and newline are control
		// characters and are allowed: a reason is a paragraph, not a label.
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
//
// The states are return_requests_status_known's CHECK. An unknown one is a
// schema change nobody carried through to here, which must be loud rather than
// rendered blank.
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
