// Package order is the vocabulary of an order: its fulfillment states and the
// back office's words for them, when one still needs paying, how long a paid
// one may wait to be picked, its event kinds and its number; it does not hold
// the buyer's side of a placed order (order/buyer).
package order

import (
	"slices"
	"strings"

	"github.com/koopa0/goen/internal/web/formtext"
)

// FulfillmentStatus is orders.fulfillment_status, closed by orders_fulfillment_status_known.
type FulfillmentStatus string

const (
	FulfillmentPending   FulfillmentStatus = "pending"
	FulfillmentPicking   FulfillmentStatus = "picking"
	FulfillmentShipped   FulfillmentStatus = "shipped"
	FulfillmentDelivered FulfillmentStatus = "delivered"
	FulfillmentCompleted FulfillmentStatus = "completed"
	FulfillmentCancelled FulfillmentStatus = "cancelled"
)

var FulfillmentStatuses = [...]FulfillmentStatus{
	FulfillmentPending,
	FulfillmentPicking,
	FulfillmentShipped,
	FulfillmentDelivered,
	FulfillmentCompleted,
	FulfillmentCancelled,
}

// Known is false for a retired value from append-only history, which must still render.
func (s FulfillmentStatus) Known() bool {
	return slices.Contains(FulfillmentStatuses[:], s)
}

// Next is the states orders_check_transition lets an order move to from s.
func (s FulfillmentStatus) Next() []FulfillmentStatus {
	switch s {
	case FulfillmentPending:
		return []FulfillmentStatus{FulfillmentPicking, FulfillmentCancelled}
	case FulfillmentPicking:
		return []FulfillmentStatus{FulfillmentShipped, FulfillmentCancelled}
	case FulfillmentShipped:
		return []FulfillmentStatus{FulfillmentDelivered, FulfillmentCompleted}
	case FulfillmentDelivered:
		return []FulfillmentStatus{FulfillmentCompleted}
	default:
		return nil
	}
}

// EventKind is order_events.kind, closed by order_events_kind_known.
type EventKind string

const (
	EventPlaced    EventKind = "placed"
	EventPaid      EventKind = "paid"
	EventPicking   EventKind = "picking"
	EventShipped   EventKind = "shipped"
	EventInTransit EventKind = "in_transit"
	EventDelivered EventKind = "delivered"
	EventCompleted EventKind = "completed"
	EventCancelled EventKind = "cancelled"
	EventRefunded  EventKind = "refunded"
)

// numberAlphabet is the suffix alphabet of an order number: Crockford base32,
// the digits and the letters without I, L, O and U. It is the one written in
// next_order_number() and orders_number_format.
const numberAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

const (
	numberPrefix = "GO-"
	numberLen    = len(numberPrefix) + 6 + 1 + 6 // GO-YYMMDD-XXXXXX
	numberDash   = len(numberPrefix) + 6
)

// ValidNumber reports whether s has the shape next_order_number() produces:
// GO-YYMMDD-XXXXXX, six digits of the day and six characters of numberAlphabet,
// matching the schema's orders_number_format CHECK. Callers concatenate it into
// a redirect, so the whole shape is validated.
func ValidNumber(s string) bool {
	if len(s) != numberLen || s[:len(numberPrefix)] != numberPrefix || s[numberDash] != '-' {
		return false
	}
	for i := len(numberPrefix); i < len(s); i++ {
		switch {
		case i == numberDash:
		case i < numberDash:
			if s[i] < '0' || s[i] > '9' {
				return false
			}
		case strings.IndexByte(numberAlphabet, s[i]) < 0:
			return false
		}
	}
	return true
}

// NormalizeNumber reads an order number as a person typed it: full-width
// characters are folded to ASCII (a Chinese keyboard left in full-width mode
// types the number it spells), surrounding space is dropped, ASCII letters are
// upper-cased, and after the GO- prefix the
// characters Crockford base32 reads as look-alikes are mapped to what they
// stand for (O to 0, I and L to 1). The prefix itself is never mapped. ok is
// false, and the number empty, when what remains is not a ValidNumber.
//
// Every path that takes a number from a person calls this, so a number typed
// from a printed page or read aloud finds the order it names.
func NormalizeNumber(s string) (number string, ok bool) {
	s = strings.TrimSpace(formtext.FoldWidth(s))
	if len(s) != numberLen {
		return "", false
	}
	b := []byte(s)
	for i, c := range b {
		if 'a' <= c && c <= 'z' {
			c -= 'a' - 'A'
		}
		if i >= len(numberPrefix) {
			switch c {
			case 'O':
				c = '0'
			case 'I', 'L':
				c = '1'
			}
		}
		b[i] = c
	}
	if n := string(b); ValidNumber(n) {
		return n, true
	}
	return "", false
}

// AwaitingPayment is the one definition of an order that still needs paying.
// 'pending' alone misses an order paid wholly from store credit or zeroed by a
// coupon: it has no payment row and owes nothing.
func AwaitingPayment(status FulfillmentStatus, committed bool, owedCents int64) bool {
	return status == FulfillmentPending && !committed && owedCents > 0
}

// PickWithinDays is the shop days a paid order may wait to be picked; the
// third day is overdue.
const PickWithinDays = 2

func PickingOverdue(days int) bool { return days > PickWithinDays }
