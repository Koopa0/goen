// Package payment takes money for an order.
//
// # Why the hosted page and not Stripe Elements
//
// goen's write-face rule is that every mutation is a plain form that works with
// scripting off. Stripe Elements puts the card field on our page and needs
// Stripe.js to submit it, which would make paying the one thing on the site
// that JavaScript owns. Stripe Checkout is a server-side API call and a
// 303 See Other, which is exactly the shape the rule asks for — and it keeps
// card data off goen entirely, so the PCI surface is a redirect.
//
// # What is trusted
//
// The browser is trusted for WHICH order it is paying and nothing else. The
// amount is recomputed from the order's own lines every time, because a form
// field carrying a price is the oldest hole there is.
//
// Stripe is trusted only through a verified webhook signature. The return to
// success_url marks nothing paid: anyone can request that URL. This is the
// single most important rule in this package.
package payment

import (
	"errors"
	"strconv"
	"time"
)

// Sentinel errors, each one a distinct decision for the handler.
var (
	// ErrNotFound is an order number that names nothing.
	ErrNotFound = errors.New("payment: order not found")
	// ErrAlreadyPaid is an order that has money against it already. The
	// customer is sent to the confirmation, not to a second checkout session.
	ErrAlreadyPaid = errors.New("payment: order already paid")
	// ErrNotPayable is an order that is no longer waiting for money —
	// cancelled, or already moved on.
	ErrNotPayable = errors.New("payment: order is not awaiting payment")
	// ErrDisabled is goen running without Stripe credentials. Browsing works;
	// paying says so plainly instead of erroring at the redirect.
	ErrDisabled = errors.New("payment: stripe is not configured")
	// ErrBadSignature is a webhook whose Stripe-Signature did not verify. It is
	// a forgery or a misconfiguration, never a retry.
	ErrBadSignature = errors.New("payment: webhook signature did not verify")
	// ErrOrderCancelled is money arriving for an order somebody called off while
	// the Checkout Session was still open at Stripe.
	//
	// It is a distinct decision because it is TERMINAL: a cancelled order is a
	// terminal fulfilment state, so no retry of this webhook can ever succeed.
	// Answering 500 and letting Stripe retry for three days would eventually get
	// the endpoint disabled — which would stop every OTHER order being captured.
	// The event is recorded, the refusal is logged with the session id, and a
	// human refunds it.
	ErrOrderCancelled = errors.New("payment: the order was cancelled before the money arrived")
)

// Currency is the only currency goen prices in.
//
// Stripe wants the amount in the currency's smallest unit. For TWD that unit is
// 1/100 of a dollar, the same unit every `_cents` column in this schema holds,
// so amounts pass through unscaled. Stripe DOES treat TWD as zero-decimal for
// manual payouts — amounts there must divide by 100 — and that rule is about
// moving money out of the Stripe balance, not about charging a customer.
// Dividing by 100 here would undercharge by 100x.
const Currency = "twd"

// integrationIdentifier labels goen's sessions in the Stripe Dashboard, so this
// checkout flow can be compared against any other goen grows later.
//
// The suffix is fixed rather than generated: it identifies the INTEGRATION, so
// it has to be the same string on every session or the Dashboard groups
// nothing together.
const integrationIdentifier = "goen-hosted-checkout-qkfmwzvt"

// MinSessionLifetime is Stripe's own floor for a Checkout Session: expires_at
// must be at least thirty minutes out or the create is refused.
//
// It is a FLOOR to check a hold against, never a duration to size a session
// from. A fixed thirty minutes added to time.Now() is false in the direction
// that costs money, however closely it matches cart.HoldTTL: the hold starts at
// PlaceOrder and the session starts when the customer presses Pay, so such a
// session is strictly LONGER than the hold behind it by however long they sat on
// the pay page — and a session that outlives its hold is a customer completing
// payment for stock the sweeper has already put back on the shelf and sold to
// somebody else. Two equal durations measured from different instants are not
// the same window.
//
// The expiry is derived from the RESERVATION instead (see [Order.SessionExpiry]),
// so the two ends of the window are one fact rather than two numbers that happen
// to match.
const MinSessionLifetime = 30 * time.Minute

// Order is what the payment page needs to know about what is being paid.
type Order struct {
	Number string
	// TotalCents is what the order still OWES, not its gross total: they differ by
	// the store credit already spent on it, and Stripe must be asked for the figure
	// this database will accept as a capture.
	TotalCents  int64
	Email       string
	Lines       []Line
	Paid        bool
	Fulfillment string
	// HoldExpiresAt is when the stock reserved for this order goes back on the
	// shelf — the earliest expiry among its live holds, and the zero time when it
	// holds nothing at all (the sweeper has already released them).
	//
	// It is what a Checkout Session's own expiry is set from, so the money cannot
	// arrive for goods that have been re-sold.
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

// FullyFunded reports whether nothing is left to pay.
//
// An order can legally owe nothing — a 100% discount, or store credit covering the
// whole of it — and orders_funded_to_leave_pending skips its payment check for
// exactly that case. Such an order must never be sent to Stripe: a zero-amount
// Checkout Session is refused by Stripe, and a non-zero one would charge money the
// customer does not owe.
func (o *Order) FullyFunded() bool { return o.TotalCents <= 0 }

// SessionExpiry is when a Checkout Session opened for this order must close.
//
// It is the stock hold's own deadline and not a duration of the payment
// package's choosing, because the thing being protected is the stock: after this
// instant the sweeper may release the hold and another customer may buy the last
// unit, so money accepted after it is money for goods goen no longer has.
func (o *Order) SessionExpiry() time.Time { return o.HoldExpiresAt }

// HoldCoversASession reports whether there is enough hold left to open a
// Checkout Session against.
//
// Stripe will not accept an expires_at less than [MinSessionLifetime] out, so
// below that there is no session goen can honestly create: one padded out to
// Stripe's floor would outlive the stock again, which is the whole defect. An
// order holding nothing (zero time) fails for the same reason — the sweeper has
// already put its stock back.
//
// This gates CREATING a session, never reusing an open one. A session already at
// Stripe carries the hold's expiry itself, so it stops being payable at exactly
// the right moment without goen refusing a customer who is five minutes from the
// deadline and one click from finishing.
func (o *Order) HoldCoversASession(now time.Time) bool {
	if o.HoldExpiresAt.IsZero() {
		return false
	}
	return !o.HoldExpiresAt.Before(now.Add(MinSessionLifetime))
}

// SessionKey is the Idempotency-Key goen sends when it creates a Checkout
// Session, and it is the SECOND line of defence against one order being charged
// twice — never the first.
//
// The first is asking the database whether a live session already exists for
// this figure. A key alone cannot be that, because the key has to change when
// the amount changes (store credit reversed, a coupon applied): the customer
// must not be sent to a session for a total the order no longer owes. So a key
// that stayed constant would be wrong and one that follows the amount leaves a
// second live session behind — which is exactly the double-charge, arriving by
// the legitimate door.
//
// What the key does cover is the CONCURRENT case the read cannot: two POSTs from
// two tabs, both reading "no live session" before either has written its payment
// row. Stripe returns the same session for both, so open_payment's
// (order_id, provider_ref) idempotence collapses them into one row.
//
// attempt is how many payment rows the order already has. It is in the key
// because a Stripe idempotency key is honoured for 24 hours: without it, a
// customer whose first session died (an asynchronous method that failed, an
// expiry) would ask for a new one, be handed back the DEAD session, and have no
// way to pay at all.
func SessionKey(number string, owedCents int64, attempt int32) string {
	return "goen-pay:" + number +
		":" + strconv.FormatInt(owedCents, 10) +
		":" + strconv.FormatInt(int64(attempt), 10)
}
