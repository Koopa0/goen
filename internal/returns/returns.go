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
	// ErrTooMany is a requested quantity above what remains returnable.
	ErrTooMany = errors.New("returns: quantity exceeds returnable amount")
	// ErrAccountErased means a concurrent erasure removed the only destination
	// for this return's store-credit payout before the request could commit.
	ErrAccountErased = errors.New("returns: account was erased before the return committed")
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

// ReturnStatus is return_requests.status, as return_requests_status_known spells it.
type ReturnStatus string

// The four states return_requests_status_known allows.
const (
	ReturnRequested ReturnStatus = "requested"
	ReturnApproved  ReturnStatus = "approved"
	ReturnRejected  ReturnStatus = "rejected"
	ReturnCompleted ReturnStatus = "completed"
)

var knownReturnStatuses = [...]ReturnStatus{
	ReturnRequested,
	ReturnApproved,
	ReturnRejected,
	ReturnCompleted,
}

// ParseDecision reads an approve/reject choice from a form. Only the two
// decision verbs are accepted; lifecycle states and typos are refused.
func ParseDecision(s string) (ReturnStatus, bool) {
	d := ReturnStatus(s)
	switch d {
	case ReturnApproved, ReturnRejected:
		return d, true
	default:
		return "", false
	}
}

// Validate refuses what the form should never have submitted. A blank reason is
// legal: Consumer Protection Act §19 I lets a consumer rescind inside seven days
// without giving one, and §19 V voids any agreement otherwise.
func (r *Request) Validate() error {
	r.Reason = strings.TrimSpace(r.Reason)
	if utf8.RuneCountInString(r.Reason) > MaxReasonRunes {
		return ErrInvalid
	}
	if strings.ContainsFunc(r.Reason, func(c rune) bool {
		return unicode.IsControl(c) && c != '\n' && c != '\t' && c != '\r'
	}) {
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

// StatusLabel is a return's state in the chrome language.
func StatusLabel(ctx context.Context, s ReturnStatus) string {
	switch s {
	case ReturnRequested:
		return i18n.T(ctx, i18n.KeyReturnStateOpen)
	case ReturnApproved:
		return i18n.T(ctx, i18n.KeyReturnStateApproved)
	case ReturnRejected:
		return i18n.T(ctx, i18n.KeyReturnStateRefused)
	case ReturnCompleted:
		return i18n.T(ctx, i18n.KeyReturnStateDone)
	default:
		return string(s)
	}
}
