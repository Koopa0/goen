// Package admin is goen's back office, served over a pool that does
// SET ROLE admin — a wider privilege set than the storefront's, still without
// direct write access to money, ledgers or stock_quantity.
package admin

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/ui/pages"
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
	// go: the decision stands and cannot be retaken, and the refund claim has
	// already committed a `pending` row keyed on the return.
	ErrRefundIncomplete = errors.New("admin: the return is approved and the refund did not complete")
	// ErrInvalid is a form goen itself rejected before the database saw it.
	ErrInvalid = errors.New("admin: invalid input")
	// ErrQuantity is a per-line count the order cannot honour: more than remains
	// to ship, or more than it still holds.
	ErrQuantity = errors.New("admin: quantity out of range")
	// ErrPaymentRequiresRefund means provider money cannot be attributed because
	// the order's stock was already returned to sale. The reconciliation alarm
	// stays open until staff refund at Stripe and choose the safe-release outcome.
	ErrPaymentRequiresRefund = errors.New("admin: payment must be refunded before reconciliation")
)

// completePaymentResolution is the operator's explicit conclusion after a
// provider-complete Checkout Session had no capture outcome goen could apply.
// Paid and safe-to-retry are financially opposite facts.
type completePaymentResolution uint8

const (
	completePaymentResolutionUnknown completePaymentResolution = iota
	// completePaymentPaid attributes the immutable payment intent as captured.
	completePaymentPaid
	// completePaymentUnpaidOrRefunded releases the gate only after staff confirm
	// that Stripe took no money or that every cent was returned.
	completePaymentUnpaidOrRefunded
)

func parseCompletePaymentResolution(s string) (completePaymentResolution, bool) {
	switch strings.TrimSpace(s) {
	case "paid":
		return completePaymentPaid, true
	case "unpaid_or_refunded":
		return completePaymentUnpaidOrRefunded, true
	default:
		return completePaymentResolutionUnknown, false
	}
}

func (r completePaymentResolution) auditValue() string {
	switch r {
	case completePaymentPaid:
		return "paid_attributed"
	case completePaymentUnpaidOrRefunded:
		return "unpaid_or_fully_refunded"
	default:
		return "invalid"
	}
}

// paymentEventSafeReleaseSubmitted recognizes the one conclusion that can
// release an unapplied provider event.
func paymentEventSafeReleaseSubmitted(s string) bool {
	return strings.TrimSpace(s) == "fully_refunded_or_accounted"
}

// PageSize bounds every admin list.
const PageSize = 50

// MinSearchRunes is the shortest order search that is a search. Counted in
// RUNES because two Chinese characters are a meaningful surname and two bytes
// are half of one.
const MinSearchRunes = 2

// statuses is the fulfilment lifecycle, in the order the queue shows it.
// orders_check_transition decides which moves are legal; parsing and the queue
// tabs consume this closed set.
var statuses = [...]struct {
	value pages.FulfillmentStatus
	label i18n.Key
}{
	{pages.FulfillmentPending, i18n.KeyAdminStatusPending},
	{pages.FulfillmentPicking, i18n.KeyAdminStatusPicking},
	{pages.FulfillmentShipped, i18n.KeyAdminStatusShipped},
	{pages.FulfillmentDelivered, i18n.KeyAdminStatusDelivered},
	{pages.FulfillmentCompleted, i18n.KeyAdminStatusCompleted},
	{pages.FulfillmentCancelled, i18n.KeyAdminStatusCancelled},
}

// ParseStatus maps a query value to a fulfilment state, or "" for all.
func ParseStatus(s string) pages.FulfillmentStatus {
	status := pages.FulfillmentStatus(s)
	if status.Known() {
		return status
	}
	return ""
}

// NextStatuses is what an order in this state may legally become.
func NextStatuses(current pages.FulfillmentStatus) []pages.FulfillmentStatus {
	switch current {
	case pages.FulfillmentPending:
		return []pages.FulfillmentStatus{pages.FulfillmentPicking, pages.FulfillmentCancelled}
	case pages.FulfillmentPicking:
		// 'shipped' is absent: [Store.Ship] is the only door, because a dispatch
		// must also settle the stock the order holds.
		return []pages.FulfillmentStatus{pages.FulfillmentCancelled}
	case pages.FulfillmentShipped:
		return []pages.FulfillmentStatus{pages.FulfillmentDelivered, pages.FulfillmentCompleted}
	case pages.FulfillmentDelivered:
		return []pages.FulfillmentStatus{pages.FulfillmentCompleted}
	default:
		return nil
	}
}

// StatusLabel is a fulfilment state in the reader's language.
//
// It answers from the status alone, which is right everywhere but 'pending' —
// see FundedStatusLabel, which the order surfaces use.
func StatusLabel(ctx context.Context, s pages.FulfillmentStatus) string {
	for _, status := range statuses {
		if status.value == s {
			return i18n.T(ctx, status.label)
		}
	}
	// Not a panic, unlike ReturnStatusLabel: a queue opening with one
	// untranslated word beats one that will not load. audit_events is
	// append-only, so a row naming a retired status must still render.
	return string(s)
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

// parseBoundedInt reads a non-negative whole number whose blank and zero forms
// both mean zero. The boolean keeps unreadable and out-of-range input distinct
// from that valid zero until the handler can render a field refusal.
func parseBoundedInt(s string, ceiling int32) (int32, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil || n < 0 || n > int64(ceiling) {
		return 0, false
	}
	return int32(n), true
}

// ReturnStatusLabel is a return request's state in the chrome language. The
// states are return_requests_status_known's CHECK.
func ReturnStatusLabel(ctx context.Context, s returns.ReturnStatus) string {
	switch s {
	case returns.ReturnRequested:
		return i18n.T(ctx, i18n.KeyAdminReturnRequested)
	case returns.ReturnApproved:
		return i18n.T(ctx, i18n.KeyAdminReturnApproved)
	case returns.ReturnRejected:
		return i18n.T(ctx, i18n.KeyAdminReturnRejected)
	case returns.ReturnCompleted:
		return i18n.T(ctx, i18n.KeyAdminReturnCompleted)
	default:
		return string(s)
	}
}

// MaxCreditGrant bounds one posting, in cents: NT$100,000. Not a schema limit,
// a fat-finger guard on a form that gives money away.
const MaxCreditGrant = 10000000

// MaxCreditReasonRunes matches the back-office form and the durable ledger.
const MaxCreditReasonRunes = 200

// FundedStatusLabel is a fulfilment state read together with what the order
// owes, which is the only way to tell the two halves of 'pending' apart: an
// order stays pending from the moment the money arrives until a human picks it,
// and one paid entirely from store credit has no payment row at all.
//
// committed means the shop has taken the order on; owed == 0 means nothing is
// due. Either is enough here: a card capture sets the first, and store credit or
// a full discount sets the second.
func FundedStatusLabel(ctx context.Context, status pages.FulfillmentStatus, committed bool, owedCents int64) string {
	if status == pages.FulfillmentPending && (committed || owedCents <= 0) {
		return i18n.T(ctx, i18n.KeyAdminStatusReadyToPick)
	}
	return StatusLabel(ctx, status)
}
