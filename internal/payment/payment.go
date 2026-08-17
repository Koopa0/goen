// Package payment takes money for an order, through Stripe hosted Checkout.
// Only a signature-verified webhook marks an order paid; the return to
// success_url marks nothing, because anyone can request that URL.
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
	// the Checkout Session was still open at Stripe. It is terminal.
	ErrOrderCancelled = errors.New("payment: the order was cancelled before the money arrived")
)

// Currency is the only currency goen prices in. TWD is not a zero-decimal
// currency for CHARGES, so _cents amounts go to Stripe unscaled.
const Currency = "twd"

const integrationIdentifier = "goen-hosted-checkout-qkfmwzvt"

// MinSessionLifetime is Stripe's floor for a Checkout Session: expires_at must
// be thirty minutes out or the create is refused. It is a floor to check a hold
// against, never a duration to size a session from.
const MinSessionLifetime = 30 * time.Minute

// Order is what the payment page needs to know about what is being paid.
type Order struct {
	Number string
	// TotalCents is what the order still OWES, not its gross total: a capture
	// for the gross is refused by payments_capture_matches_order.
	TotalCents  int64
	Email       string
	Lines       []Line
	Paid        bool
	Fulfillment string
	// HoldExpiresAt is the earliest expiry among the order's live stock holds.
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

// FullyFunded reports whether nothing is left to pay; Stripe refuses a
// zero-amount session, so such an order is never sent to it.
func (o *Order) FullyFunded() bool { return o.TotalCents <= 0 }

// SessionExpiry is when a Checkout Session opened for this order must close: the
// stock hold's own deadline, so money cannot arrive for goods already re-sold.
func (o *Order) SessionExpiry() time.Time { return o.HoldExpiresAt }

// HoldCoversASession reports whether there is enough hold left to open a
// Checkout Session against.
func (o *Order) HoldCoversASession(now time.Time) bool {
	if o.HoldExpiresAt.IsZero() {
		return false
	}
	return !o.HoldExpiresAt.Before(now.Add(MinSessionLifetime))
}

// SessionKey is the Idempotency-Key goen sends when it creates a Checkout
// Session. Stripe honours a key for 24 hours, which is what attempt is in it for.
func SessionKey(number string, owedCents int64, attempt int32) string {
	return "goen-pay:" + number +
		":" + strconv.FormatInt(owedCents, 10) +
		":" + strconv.FormatInt(int64(attempt), 10)
}
