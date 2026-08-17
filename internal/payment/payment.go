// Package payment takes money for an order, through Stripe hosted Checkout.
//
// The browser is trusted for WHICH order it is paying and nothing else; the
// amount is recomputed from the order's lines. Only a signature-verified
// webhook marks an order paid — the return to success_url marks nothing,
// because anyone can request that URL.
package payment

import (
	"errors"
	"strconv"
	"time"
)

var (
	// ErrNotFound is an order number that names nothing.
	ErrNotFound = errors.New("payment: order not found")
	// ErrAlreadyPaid is an order that has money against it already.
	ErrAlreadyPaid = errors.New("payment: order already paid")
	// ErrNotPayable is an order that is no longer waiting for money.
	ErrNotPayable = errors.New("payment: order is not awaiting payment")
	// ErrDisabled is goen running without Stripe credentials.
	ErrDisabled = errors.New("payment: stripe is not configured")
	// ErrBadSignature is a webhook whose Stripe-Signature did not verify.
	ErrBadSignature = errors.New("payment: webhook signature did not verify")
	// ErrOrderCancelled is money arriving for an order somebody called off while
	// the Checkout Session was still open at Stripe. It is terminal — no retry
	// can succeed — so the webhook records it, answers 200, and a human refunds.
	ErrOrderCancelled = errors.New("payment: the order was cancelled before the money arrived")
)

// Currency is the only currency goen prices in.
//
// Stripe wants the smallest unit, which for TWD CHARGES is 1/100 dollar — the
// unit every _cents column holds, so amounts pass unscaled. The zero-decimal
// rule for TWD applies to manual payouts; dividing here undercharges by 100x.
const Currency = "twd"

// integrationIdentifier labels goen's sessions in the Stripe Dashboard. Fixed
// rather than generated: it identifies the integration, not the session.
const integrationIdentifier = "goen-hosted-checkout-qkfmwzvt"

// MinSessionLifetime is Stripe's own floor for a Checkout Session: expires_at
// must be at least thirty minutes out or the create is refused.
//
// It is a FLOOR to check a hold against, never a duration to size a session
// from — see [Order.SessionExpiry].
const MinSessionLifetime = 30 * time.Minute

// Order is what the payment page needs to know about what is being paid.
type Order struct {
	Number string
	// TotalCents is what the order still OWES, not its gross total: the two differ
	// by the store credit already spent on it, and a capture for the gross is
	// refused by payments_capture_matches_order after the money has been taken.
	TotalCents  int64
	Email       string
	Lines       []Line
	Paid        bool
	Fulfillment string
	// HoldExpiresAt is the earliest expiry among this order's live stock holds,
	// and the zero time when it holds nothing at all.
	HoldExpiresAt time.Time
}

// Line is one item as the ORDER recorded it, not as the catalogue reads today.
type Line struct {
	Name      string
	Label     string
	UnitCents int64
	Quantity  int32
}

// Capture is what a verified webhook says Stripe took.
type Capture struct {
	SessionID  string
	AmountRecv int64
	CardBrand  string
	CardLast4  string
}

// WebhookEvent is a verified Stripe event, ready to be recorded.
type WebhookEvent struct {
	ID        string
	Type      string
	ObjectRef string
	Payload   []byte
}

// FullyFunded reports whether nothing is left to pay. Such an order is never
// sent to Stripe, which refuses a zero-amount session.
func (o *Order) FullyFunded() bool { return o.TotalCents <= 0 }

// SessionExpiry is when a Checkout Session opened for this order must close: the
// stock hold's own deadline, so money cannot arrive for goods already re-sold.
func (o *Order) SessionExpiry() time.Time { return o.HoldExpiresAt }

// HoldCoversASession reports whether there is enough hold left to open a
// Checkout Session against.
//
// It gates CREATING a session, never reusing an open one: a session already at
// Stripe carries the hold's own expiry.
func (o *Order) HoldCoversASession(now time.Time) bool {
	if o.HoldExpiresAt.IsZero() {
		return false
	}
	return !o.HoldExpiresAt.Before(now.Add(MinSessionLifetime))
}

// SessionKey is the Idempotency-Key goen sends when it creates a Checkout
// Session, collapsing two concurrent POSTs into one session at Stripe.
//
// attempt is how many payment rows the order already has: Stripe honours a key
// for 24 hours, so without it a customer whose first session died is handed the
// dead one back and cannot pay at all.
func SessionKey(number string, owedCents int64, attempt int32) string {
	return "goen-pay:" + number +
		":" + strconv.FormatInt(owedCents, 10) +
		":" + strconv.FormatInt(int64(attempt), 10)
}
