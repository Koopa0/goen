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
	ErrNotFound      = errors.New("returns: order not found")
	ErrNotReturnable = errors.New("returns: nothing on this order can be returned")
	ErrAlreadyOpen   = errors.New("returns: this order already has an open request")
	ErrInvalid       = errors.New("returns: invalid request")
	ErrTooMany       = errors.New("returns: quantity exceeds returnable amount")
	// ErrAccountErased means a concurrent erasure removed the only destination
	// for the return's store-credit payout before the request could commit.
	ErrAccountErased = errors.New("returns: account was erased before the return committed")
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

type Status string

// The four states return_requests_refund_snapshot_shape allows.
const (
	StatusRequested Status = "requested"
	StatusApproved  Status = "approved"
	StatusRejected  Status = "rejected"
	StatusCompleted Status = "completed"
)

var knownStatuses = [...]Status{
	StatusRequested,
	StatusApproved,
	StatusRejected,
	StatusCompleted,
}

// Validate allows a blank reason: Consumer Protection Act §19 I lets a consumer
// rescind inside seven days without giving one, and §19 V voids any agreement
// otherwise.
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

func StatusLabel(ctx context.Context, s Status) string {
	switch s {
	case StatusRequested:
		return i18n.T(ctx, i18n.KeyReturnStateOpen)
	case StatusApproved:
		return i18n.T(ctx, i18n.KeyReturnStateApproved)
	case StatusRejected:
		return i18n.T(ctx, i18n.KeyReturnStateRefused)
	case StatusCompleted:
		return i18n.T(ctx, i18n.KeyReturnStateDone)
	default:
		return string(s)
	}
}
