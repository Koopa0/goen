package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	stripe "github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/webhook"

	"github.com/koopa0/goen/internal/i18n"
)

// Gateway is the only thing in goen that knows Stripe exists. Everything above
// it deals in [Order] and [Capture].
type Gateway struct {
	client        *stripe.Client
	webhookSecret string
	baseURL       string
}

// NewGateway wires Stripe. A blank secret key is not an error: goen browses and
// sells fine without it, and the payment page says 金流尚未啟用 rather than
// failing at the redirect. A blank WEBHOOK secret with a live key IS an error,
// because it would leave the endpoint that marks orders paid unauthenticated.
func NewGateway(secretKey, webhookSecret, baseURL string) (*Gateway, error) {
	if secretKey == "" {
		return &Gateway{baseURL: baseURL}, nil
	}
	if webhookSecret == "" {
		return nil, errors.New("payment: a Stripe secret key without a webhook secret " +
			"would leave /webhooks/stripe unauthenticated")
	}
	if _, err := url.Parse(baseURL); err != nil || !strings.HasPrefix(baseURL, "http") {
		return nil, fmt.Errorf("payment: base URL %q is not usable for Stripe return URLs", baseURL)
	}
	return &Gateway{
		client:        stripe.NewClient(secretKey),
		webhookSecret: webhookSecret,
		baseURL:       strings.TrimRight(baseURL, "/"),
	}, nil
}

// Enabled reports whether goen can actually take money.
func (g *Gateway) Enabled() bool { return g.client != nil }

// StartSession creates a Stripe Checkout Session for an order and returns the
// session id and the URL to send the customer to.
//
// The amount comes from o, which the store recomputed from the order's lines.
// Nothing here reads a form field.
//
// attempt is how many payments the order has already had; it is part of the
// idempotency key, and the caller reads it in the same statement that decided
// there was no live session to reuse. See [SessionKey].
func (g *Gateway) StartSession(ctx context.Context, o *Order, attempt int32) (id, redirectURL string, err error) {
	if !g.Enabled() {
		return "", "", ErrDisabled
	}

	items := make([]*stripe.CheckoutSessionCreateLineItemParams, 0, len(o.Lines)+1)
	var lineTotal int64
	for i := range o.Lines {
		l := &o.Lines[i]
		lineTotal += l.UnitCents * int64(l.Quantity)
		items = append(items, &stripe.CheckoutSessionCreateLineItemParams{
			Quantity: stripe.Int64(int64(l.Quantity)),
			PriceData: &stripe.CheckoutSessionCreateLineItemPriceDataParams{
				Currency:   stripe.String(Currency),
				UnitAmount: stripe.Int64(l.UnitCents),
				ProductData: &stripe.CheckoutSessionCreateLineItemPriceDataProductDataParams{
					Name: stripe.String(displayName(l)),
				},
			},
		})
	}

	// Shipping, tax and discount are the difference between the lines and the
	// order total. Stripe's page must add up to the same figure goen will
	// reconcile against, so the remainder is sent as its own line rather than
	// silently dropped — a hosted page that shows a different total from the
	// confirmation email is a support ticket every time.
	if rest := o.TotalCents - lineTotal; rest != 0 {
		if rest < 0 {
			return "", "", fmt.Errorf("payment: order %s totals %d below its lines' %d",
				o.Number, o.TotalCents, lineTotal)
		}
		items = append(items, &stripe.CheckoutSessionCreateLineItemParams{
			Quantity: stripe.Int64(1),
			PriceData: &stripe.CheckoutSessionCreateLineItemPriceDataParams{
				Currency:   stripe.String(Currency),
				UnitAmount: stripe.Int64(rest),
				ProductData: &stripe.CheckoutSessionCreateLineItemPriceDataProductDataParams{
					Name: stripe.String(i18n.T(ctx, i18n.KeyShippingAndTax)),
				},
			},
		})
	}

	params := &stripe.CheckoutSessionCreateParams{
		Mode:      stripe.String(string(stripe.CheckoutSessionModePayment)),
		LineItems: items,
		// Stripe's own page, in the visitor's language. A locale pinned to one
		// language makes the payment step the one place a visitor is handed a
		// form they cannot read — and it is the step where being unsure what you
		// are agreeing to matters most.
		Locale: stripe.String(i18n.FromContext(ctx).StripeTag()),
		// success_url is where the browser lands, NOT where the order is marked
		// paid. It carries no session token for that reason: there is nothing on
		// this page worth forging.
		SuccessURL: stripe.String(g.baseURL + "/orders/" + url.PathEscape(o.Number) + "?paid=1"),
		CancelURL:  stripe.String(g.baseURL + "/orders/" + url.PathEscape(o.Number) + "/pay?cancelled=1"),
		// The session dies with the STOCK HOLD, not thirty minutes from now.
		// Thirty minutes from this line and thirty minutes from PlaceOrder are
		// different instants however equal the two durations look, so a session
		// sized from here outlives the goods behind it by however long the
		// customer sat on the pay page — long enough for the sweeper to release
		// the stock and somebody else to buy it while the checkout is still
		// payable. The caller refuses to get this far when there is not enough
		// hold left for Stripe's own floor.
		ExpiresAt: stripe.Int64(o.SessionExpiry().Unix()),
		// The order number travels to Stripe so a human reconciling the
		// dashboard against goen has the join key in front of them.
		ClientReferenceID: stripe.String(o.Number),
		Metadata:          map[string]string{"order_number": o.Number},
		// Labels every session as coming from this flow, so the Dashboard can
		// compare it against any other checkout goen grows later. Available
		// from API version 2026-03-25.dahlia; the pinned SDK is newer.
		IntegrationIdentifier: stripe.String(integrationIdentifier),
	}

	// payment_method_types is PINNED rather than omitted. Omitting it lets the
	// Dashboard decide — per currency, per country, per amount — which methods a
	// customer sees, and that reasoning is sound on its own while CONTRADICTING
	// the ExpiresAt decision ten lines above, which pins the session to the stock
	// hold precisely so money cannot arrive after the goods are back on the shelf.
	//
	// A DELAYED method breaks that. Its `checkout.session.completed` arrives with
	// payment_status `unpaid` and the funds settle days later as
	// `async_payment_succeeded` — after the session is over, so ExpiresAt bounds
	// nothing, and a completed session never fires `checkout.session.expired`, so
	// the abandoned path never runs either. The hold is swept 30 minutes after
	// PlaceOrder, the units are re-sold, and the capture then succeeds against
	// stock that is gone. Nothing raises: capture_payment does not read
	// reservations and admin.Ship ranges over an empty held-reservation slice.
	//
	// Two halves each correct and disagreeing, which is the shape CLAUDE.md's
	// mistake #13 records — and no guard here can see it, because every one of
	// them asks what is ABSENT.
	//
	// Card is what goen's 30-minute hold can survive, so card is what it offers.
	// Apple Pay and Google Pay ride on this type. Supporting a delayed method is
	// a FEATURE, not a flag: it needs a hold whose life is the payment deadline,
	// a sweeper that spares an order with money in flight, and a shop decision
	// about how many days of stock an unpaid transfer may hold.
	params.PaymentMethodTypes = stripe.StringSlice([]string{"card"})

	if o.Email != "" {
		params.CustomerEmail = stripe.String(o.Email)
	}

	// Two POSTs that both read "no live session" before either wrote its payment
	// row get ONE session out of Stripe, so open_payment collapses them into one
	// row instead of opening a second checkout for the same order.
	params.SetIdempotencyKey(SessionKey(o.Number, o.TotalCents, attempt))

	sess, err := g.client.V1CheckoutSessions.Create(ctx, params)
	if err != nil {
		return "", "", fmt.Errorf("create checkout session for order %s: %w", o.Number, err)
	}
	return sess.ID, sess.URL, nil
}

// ResumeSession reports where to send a customer who already has a Checkout
// Session open, and whether it is still open at all.
//
// The URL cannot be recovered from goen's own records: payments stores the
// session id and Stripe's checkout URL carries a fragment that is not derivable
// from it, so the only way to send somebody back to a session they already have
// is to ask Stripe for it.
//
// open is false for a session Stripe has finished with — paid, still processing
// an asynchronous method, or expired. NONE of those is a reason to create a
// second session: the first two may have money in flight, and the third means
// the stock hold has gone. An error is not a reason either, which is why it is
// returned rather than swallowed — creating a session because the check failed
// is the double charge arriving through the code that exists to prevent it.
func (g *Gateway) ResumeSession(ctx context.Context, sessionID string) (redirectURL string, open bool, err error) {
	if !g.Enabled() {
		return "", false, ErrDisabled
	}
	sess, err := g.client.V1CheckoutSessions.Retrieve(ctx, sessionID, nil)
	if err != nil {
		return "", false, fmt.Errorf("read checkout session %s: %w", sessionID, err)
	}
	if sess.Status != stripe.CheckoutSessionStatusOpen {
		return "", false, nil
	}
	return sess.URL, true, nil
}

// ExpireSession closes a Checkout Session at Stripe so nobody can pay it.
//
// Cancelling an order releases its stock and returns its store credit, and
// without this it would leave the checkout the customer may still have open in
// another tab PAYABLE. The sequence "cancel, then finish paying on the tab that
// is still there" then puts money against an order that has been called off and
// whose goods are back on the shelf. [ErrOrderCancelled] is the name goen has
// for that money arriving, and its own comment ends "a human refunds it"; this
// is what stops it happening rather than describing it afterwards.
//
// **Whether there is money in flight is Stripe's question, not goen's.** Stripe
// expires an OPEN session and refuses anything else, so a session the customer
// completed one second ago is refused here and the capture goes through as it
// should. goen deciding for itself would mean reading a payment row that the
// webhook has not updated yet and calling it empty — the same mistake as
// trusting the event for which order it belongs to. The refusal is returned and
// the caller logs it; it is not an error worth failing a cancellation over,
// because the cancellation has already committed and is correct.
func (g *Gateway) ExpireSession(ctx context.Context, sessionID string) error {
	if !g.Enabled() {
		return ErrDisabled
	}
	if _, err := g.client.V1CheckoutSessions.Expire(ctx, sessionID,
		&stripe.CheckoutSessionExpireParams{}); err != nil {
		return fmt.Errorf("expire checkout session %s: %w", sessionID, err)
	}
	return nil
}

// displayName is what Stripe's page calls a line. The variant label is folded
// in because "耳機" alone does not tell a customer which one they are buying.
func displayName(l *Line) string {
	if l.Label == "" {
		return l.Name
	}
	return l.Name + "(" + l.Label + ")"
}

// VerifyWebhook checks a webhook's signature and returns the event.
//
// This is the whole security boundary for the endpoint that marks orders paid.
// The body must be the RAW bytes: the signature is over them, so anything that
// re-encodes the JSON first breaks verification — which is why the handler
// reads the body itself instead of decoding it.
func (g *Gateway) VerifyWebhook(body []byte, sigHeader string) (stripe.Event, error) {
	if !g.Enabled() {
		return stripe.Event{}, ErrDisabled
	}
	// The API-version check is off on purpose. Stripe upgrades an account's
	// version independently of this binary, and a mismatch is not a reason to
	// stop recording captures — the fields this package reads have been stable
	// for years. The SIGNATURE and the timestamp tolerance are not relaxed.
	ev, err := webhook.ConstructEventWithOptions(body, sigHeader, g.webhookSecret,
		webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
	if err != nil {
		return stripe.Event{}, fmt.Errorf("%w: %w", ErrBadSignature, err)
	}
	return ev, nil
}

// captureEvents are the two events that can carry money goen must record.
//
// The session PINS payment_method_types to card — see [Gateway.StartSession],
// where the reasoning is — so a delayed method should not arise at all, and
// [UnsettledSessionFrom] reports at ERROR if one ever does.
// `checkout.session.async_payment_succeeded` is here as defence in depth for
// exactly that case. A delayed method's `completed` event arrives with
// payment_status `unpaid`, which the check below refuses and
// TestOnlyAPaidSessionIsACapture holds; the money clearing days later arrives as
// async_payment_succeeded and as nothing else. A reader that accepts only
// `completed` therefore logs that success, answers 200, and leaves the customer
// paid with the order unpaid forever.
//
// The payload is a checkout.session either way, so the same reader serves both.
var captureEvents = map[stripe.EventType]bool{
	"checkout.session.completed":               true,
	"checkout.session.async_payment_succeeded": true,
}

// CaptureFrom reads a paid-checkout event into a [Capture], or reports that the
// event is not one goen acts on.
//
// Only a session with payment_status `paid` is a capture. An event for an unpaid
// or expired session is recorded and ignored: acting on the event TYPE alone
// would mark an order paid for a session the customer abandoned at the card
// field, and for the delayed methods above it would mark one paid days before
// the transfer clears.
func CaptureFrom(ev *stripe.Event) (Capture, bool) {
	if !captureEvents[ev.Type] {
		return Capture{}, false
	}
	var sess stripe.CheckoutSession
	if err := json.Unmarshal(ev.Data.Raw, &sess); err != nil {
		return Capture{}, false
	}
	if sess.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid {
		return Capture{}, false
	}
	if sess.ID == "" || sess.AmountTotal <= 0 {
		return Capture{}, false
	}
	c := Capture{SessionID: sess.ID, AmountRecv: sess.AmountTotal}
	if pi := sess.PaymentIntent; pi != nil && pi.LatestCharge != nil {
		if d := pi.LatestCharge.PaymentMethodDetails; d != nil && d.Card != nil {
			c.CardBrand = string(d.Card.Brand)
			c.CardLast4 = d.Card.Last4
		}
	}
	return c, true
}

// abandonedEvents are the two ways a Checkout Session ends with no money.
//
// `checkout.session.async_payment_failed` is the other half of
// async_payment_succeeded: a delayed method that did not clear. It belongs here
// rather than beside the captures because the outcome is the session's, not the
// money's — nothing arrived, and the row goen opened has to stop saying it is
// waiting for something.
var abandonedEvents = map[stripe.EventType]bool{
	"checkout.session.expired":              true,
	"checkout.session.async_payment_failed": true,
}

// AbandonedSessionFrom reports the session id of a Checkout session that ended
// with no money.
//
// Stripe expires a session the customer never completed, and something has to
// listen: with nothing reading these events the payment row goen opened stays at
// requires_payment forever, so reconciliation cannot tell an abandoned checkout
// from one still in flight. cancel_payment is the door that closes it.
//
// A session that expired AFTER being paid does not exist, but the guard is
// cheap and cancel_payment refuses a succeeded row anyway: the state is decided
// by the payment, never by the event.
func AbandonedSessionFrom(ev *stripe.Event) (string, bool) {
	if !abandonedEvents[ev.Type] {
		return "", false
	}
	var sess stripe.CheckoutSession
	if err := json.Unmarshal(ev.Data.Raw, &sess); err != nil {
		return "", false
	}
	if sess.ID == "" {
		return "", false
	}
	return sess.ID, true
}

// UnsettledSessionFrom reports the session id of a checkout the customer
// FINISHED while the money is still on its way.
//
// It is the one signal Stripe gives that a delayed payment method is in play,
// and goen's stock model cannot survive one — the reasoning is on the
// PaymentMethodTypes pin in [Gateway.StartSession]. Because that pin makes card
// the only method offered, an event reaching here is a CONFIGURATION change
// rather than anything a customer did, which is why the handler reports it at
// ERROR rather than recording it and moving on.
//
// It deliberately writes NOTHING. Capturing would be the defect; extending the
// hold is a feature that is not built and needs a shop decision first. What this
// buys is that the condition is not silent: with no reader of its own, a
// completed-but-unpaid session falls to the handler's default branch and is
// logged as one more event goen does not act on, indistinguishable from the
// dozen it genuinely does not act on.
//
// `no_payment_required` is not this case and does not reach Stripe at all: a
// zero-owed order is never sent, because Stripe refuses a zero-amount session.
func UnsettledSessionFrom(ev *stripe.Event) (string, bool) {
	if ev.Type != "checkout.session.completed" {
		return "", false
	}
	var sess stripe.CheckoutSession
	if err := json.Unmarshal(ev.Data.Raw, &sess); err != nil {
		return "", false
	}
	if sess.ID == "" || sess.PaymentStatus != stripe.CheckoutSessionPaymentStatusUnpaid {
		return "", false
	}
	return sess.ID, true
}

// ObjectRef is the Stripe id an event is about, for the audit row. Best effort:
// the row is worth writing even when the id cannot be read.
func ObjectRef(ev *stripe.Event) string {
	if id, ok := ev.Data.Object["id"].(string); ok {
		return id
	}
	return ""
}

// EventAge is how old Stripe says the event is, used only for logging. Kept as
// a helper so the handler does not do arithmetic on a unix timestamp inline.
func EventAge(ev *stripe.Event) string {
	if ev.Created == 0 {
		return "unknown"
	}
	return strconv.FormatInt(time.Now().Unix()-ev.Created, 10) + "s"
}
