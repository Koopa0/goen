// Package admin is goen's back office, served over a pool that does
// SET ROLE admin — a wider privilege set than the storefront's, still without
// direct write access to money, ledgers or stock_quantity.
package admin

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

var (
	// ErrNotFound is an order, product or variant that does not exist.
	ErrNotFound = errors.New("admin: not found")
	// ErrForbidden is a signed-in customer who is not staff.
	ErrForbidden = errors.New("admin: forbidden")
	// ErrRefused is a write the database declined; its message is the database's
	// own, because that names the rule.
	ErrRefused = errors.New("admin: refused")

	// ErrRefundIncomplete is a return that WAS approved and whose money did not
	// go. It is its own sentinel because it sends a staff member somewhere
	// different from every other refusal: the decision stands and cannot be
	// retaken, and what is outstanding is a payment. open_refund has already
	// committed a `pending` row keyed on the return, so the attempt is on
	// record rather than lost.
	ErrRefundIncomplete = errors.New("admin: the return is approved and the refund did not complete")
	// ErrInvalid is a form goen itself rejected before the database saw it.
	ErrInvalid = errors.New("admin: invalid input")
	// ErrQuantity is a per-line count the order cannot honour: more than remains
	// to ship, or more than it still holds.
	ErrQuantity = errors.New("admin: quantity out of range")
)

// PageSize bounds every admin list.
const PageSize = 50

// MinSearchRunes is the shortest order search that is a search. Counted in
// RUNES because two Chinese characters are a meaningful surname and two bytes
// are half of one.
const MinSearchRunes = 2

// Statuses is the fulfilment lifecycle, in the order the queue shows it.
// orders_check_transition decides what is legal; this only renders tabs.
var Statuses = []string{"pending", "picking", "shipped", "delivered", "completed", "cancelled"}

// ParseStatus maps a query value to a fulfilment state, or "" for all.
func ParseStatus(s string) string {
	if slices.Contains(Statuses, s) {
		return s
	}
	return ""
}

// NextStatuses is what an order in this state may legally become.
func NextStatuses(current string) []string {
	switch current {
	case "pending":
		return []string{"picking", "cancelled"}
	case "picking":
		// 'shipped' is absent: [Store.Ship] is the only door, because a dispatch
		// must also settle the stock the order holds.
		return []string{"cancelled"}
	case "shipped":
		return []string{"delivered", "completed"}
	case "delivered":
		return []string{"completed"}
	default:
		return nil
	}
}

// StatusLabel is a fulfilment state in the reader's language.
//
// It answers from the status alone, which is right everywhere but 'pending' —
// see FundedStatusLabel, which the order surfaces use.
func StatusLabel(ctx context.Context, s string) string {
	switch s {
	case "pending":
		return i18n.T(ctx, i18n.KeyAdminStatusPending)
	case "picking":
		return i18n.T(ctx, i18n.KeyAdminStatusPicking)
	case "shipped":
		return i18n.T(ctx, i18n.KeyAdminStatusShipped)
	case "delivered":
		return i18n.T(ctx, i18n.KeyAdminStatusDelivered)
	case "completed":
		return i18n.T(ctx, i18n.KeyAdminStatusCompleted)
	case "cancelled":
		return i18n.T(ctx, i18n.KeyAdminStatusCancelled)
	default:
		// A queue that opens with one untranslated word beats one that will not
		// load, which is why this does not panic as its two neighbours do.
		return s
	}
}

// IsOrderNumber reports whether s has the shape next_order_number() produces:
// GO-YYMMDD-NNNNNN, matching the schema's orders_number_format CHECK. Callers
// concatenate it into a redirect, so the whole shape is validated.
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

// maxAdjustment bounds one stock correction: large enough for a delivery,
// small enough that a typo cannot invent a warehouse.
const maxAdjustment = 10000

// ParseAdjustment reads a stock delta. Zero is not an adjustment.
func ParseAdjustment(s string) (int32, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
	if err != nil || n == 0 || n > maxAdjustment || n < -maxAdjustment {
		return 0, false
	}
	return int32(n), true
}

// ParseReceipt reads a goods-receipt quantity, which is always POSITIVE.
// inventory_movements_delta_direction stays the authority; this turns a mistyped
// minus sign into a form the shop can correct rather than a constraint name.
func ParseReceipt(s string) (int32, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
	if err != nil || n <= 0 || n > maxAdjustment {
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

// ReturnStatusLabel is a return request's state in the chrome language. The
// states are return_requests_status_known's CHECK.
func ReturnStatusLabel(ctx context.Context, s string) string {
	switch s {
	case "requested":
		return i18n.T(ctx, i18n.KeyAdminReturnRequested)
	case "approved":
		return i18n.T(ctx, i18n.KeyAdminReturnApproved)
	case "rejected":
		return i18n.T(ctx, i18n.KeyAdminReturnRejected)
	case "completed":
		return i18n.T(ctx, i18n.KeyAdminReturnCompleted)
	default:
		panic("admin: no label for return status " + s)
	}
}

// MaxCreditGrant bounds one posting, in cents: NT$100,000. Not a schema limit,
// a fat-finger guard on a form that gives money away.
const MaxCreditGrant = 10000000

// FundedStatusLabel is a fulfilment state read together with what the order
// owes, which is the only way to tell the two halves of 'pending' apart.
//
// An order stays pending from the moment the money arrives until a human picks
// it, and one paid entirely from store credit has no payment row at all and sits
// there for good — so the status alone badged a paid order 待付款, on the queue
// somebody works and beside the customer's own page saying 付款完成. Nothing will
// ever move it, because no payment is coming.
//
// committed means the shop has taken the order on; owed == 0 means nothing is
// due. Either is enough here: a card capture sets the first, and store credit or
// a full discount sets the second.
func FundedStatusLabel(ctx context.Context, status string, committed bool, owedCents int64) string {
	if status == "pending" && (committed || owedCents <= 0) {
		return i18n.T(ctx, i18n.KeyAdminStatusReadyToPick)
	}
	return StatusLabel(ctx, status)
}
