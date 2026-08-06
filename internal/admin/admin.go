// Package admin is goen's back office.
//
// It connects through its own pool, which does SET ROLE admin. That is a WIDER
// privilege set than the storefront's, not an unbounded one: an admin still
// cannot write a payment, a ledger row, or stock_quantity by hand. Stock moves
// through record_inventory_movement so every adjustment lands in the ledger
// with a reason, and the database — not this package — is what enforces it.
package admin

import (
	"errors"
	"strconv"
	"strings"
)

// Errors a handler branches on.
var (
	// ErrNotFound is an order or variant that does not exist.
	ErrNotFound = errors.New("admin: not found")
	// ErrForbidden is a signed-in customer who is not staff.
	ErrForbidden = errors.New("admin: forbidden")
	// ErrRefused is a write the database declined — an illegal transition, an
	// adjustment that would breach the stock floor. The message is the
	// database's own, because it names the rule.
	ErrRefused = errors.New("admin: refused")
	// ErrInvalid is a form goen itself rejected before the database saw it — a
	// blank carrier or tracking number. Distinct from ErrRefused because it is
	// the staff member's input to fix, not the order's state.
	ErrInvalid = errors.New("admin: invalid input")
)

// PageSize bounds every admin list. A back office reads a queue, not an
// archive; anything longer is a report.
const PageSize = 50

// MinSearchRunes is the shortest order search that is a search.
//
// Below two characters a prefix matches most of the table, which is a list rather
// than an answer — and it is a list produced by scanning order history. Counted in
// RUNES because two Chinese characters are a meaningful surname and two bytes are
// half of one.
const MinSearchRunes = 2

// Statuses is the fulfilment lifecycle, in the order the queue shows it.
//
// The transitions themselves are the schema's: orders_check_transition knows
// the state machine and refuses an illegal move, so this list is for rendering
// tabs, not for deciding what is legal.
var Statuses = []string{"pending", "picking", "shipped", "delivered", "completed", "cancelled"}

// ParseStatus maps a query value to a fulfilment state, or "" for all.
func ParseStatus(s string) string {
	for _, known := range Statuses {
		if s == known {
			return s
		}
	}
	return ""
}

// NextStatuses is what an order in this state may legally become.
//
// It mirrors orders_check_transition so the page offers only moves the database
// will accept — a button that leads to a refusal is worse than no button. The
// database remains the authority: this list being wrong shows up as a missing
// option, never as an illegal write.
func NextStatuses(current string) []string {
	switch current {
	case "pending":
		return []string{"picking", "cancelled"}
	case "picking":
		// 'shipped' is deliberately absent. Dispatching needs a carrier and a
		// tracking number, and it must also settle the stock the order was
		// holding — see admin.Store.Ship. Offering it here as a bare status
		// change would produce an order marked shipped with no shipment row and
		// reservations still held, which a sweeper would later return to the
		// shelf and oversell. The ship form is the only door.
		return []string{"cancelled"}
	case "shipped":
		return []string{"delivered", "completed"}
	case "delivered":
		return []string{"completed"}
	default:
		// completed and cancelled are terminal.
		return nil
	}
}

// StatusLabel is a fulfilment state in the chrome language.
func StatusLabel(s string) string {
	switch s {
	case "pending":
		return "待付款"
	case "picking":
		return "備貨中"
	case "shipped":
		return "已出貨"
	case "delivered":
		return "已送達"
	case "completed":
		return "已完成"
	case "cancelled":
		return "已取消"
	default:
		return s
	}
}

// IsOrderNumber reports whether s has the shape next_order_number() produces:
// GO-YYMMDD-NNNNNN, matching the schema's orders_number_format CHECK.
//
// It exists so a value taken from the request path is never concatenated into a
// redirect on trust. A path segment cannot contain a slash, so it could not
// become "//host" — but it could carry a newline or a query separator, and
// "validate what it must look like" is cheaper to be sure of than "enumerate
// what it must not".
func IsOrderNumber(s string) bool {
	if len(s) != 16 || s[:3] != "GO-" || s[9] != '-' {
		return false
	}
	for i, r := range s {
		if i == 0 || i == 1 || i == 2 || i == 9 {
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// maxAdjustment bounds one stock correction. Large enough for a delivery,
// small enough that a typo cannot invent a warehouse.
const maxAdjustment = 10000

// ParseAdjustment reads a stock delta. Zero is not an adjustment, so it is
// refused rather than written as a no-op movement.
func ParseAdjustment(s string) (int32, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
	if err != nil || n == 0 || n > maxAdjustment || n < -maxAdjustment {
		return 0, false
	}
	return int32(n), true
}

// ParsePrice reads a price in whole New Taiwan dollars and returns minor units.
func ParsePrice(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, true // an empty compare-at price means "not on sale"
	}
	n, err := strconv.ParseInt(s, 10, 64)
	const maxTWD = 100_000_000 // the schema's own ceiling, in dollars
	if err != nil || n < 0 || n > maxTWD {
		return 0, false
	}
	return n * 100, true
}

// ReturnStatusLabel is a return request's state in the chrome language.
//
// The states are return_requests_status_known's CHECK. An unknown one is a
// schema change nobody carried through here, which must be loud rather than
// rendered blank.
func ReturnStatusLabel(s string) string {
	switch s {
	case "requested":
		return "待處理"
	case "approved":
		return "已同意"
	case "rejected":
		return "未同意"
	case "completed":
		return "已完成"
	default:
		panic("admin: no label for return status " + s)
	}
}

// MaxCreditGrant bounds one posting, in cents.
//
// NT$100,000. Not a schema limit — store_credit_entries allows far more — but a
// fat-finger guard on a form that gives money away: a staff member who means
// 500 and types 500000 should meet a refusal, not a customer with a windfall.
const MaxCreditGrant = 10000000
