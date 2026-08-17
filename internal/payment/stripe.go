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

// NewGateway wires Stripe. A blank secret key is not an error — the site still
// sells and the payment page says so — but a key without a webhook secret is,
// because it leaves the endpoint that marks orders paid unauthenticated.
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
// session id and the URL to send the customer to. attempt is how many payments
// the order has already had — see [SessionKey].
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
	// order total, and Stripe's page must add up to the figure goen reconciles
	// against — so the remainder goes as its own line rather than being dropped.
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
		Locale:    stripe.String(i18n.FromContext(ctx).StripeTag()),
		// success_url is where the browser lands, NOT where the order is marked
		// paid, so it carries no token: there is nothing here worth forging.
		SuccessURL: stripe.String(g.baseURL + "/orders/" + url.PathEscape(o.Number) + "?paid=1"),
		CancelURL:  stripe.String(g.baseURL + "/orders/" + url.PathEscape(o.Number) + "/pay?cancelled=1"),
		// The session dies with the STOCK HOLD, not thirty minutes from now: a
		// window measured from here outlives the goods by however long the
		// customer sat on the pay page.
		ExpiresAt:             stripe.Int64(o.SessionExpiry().Unix()),
		ClientReferenceID:     stripe.String(o.Number),
		Metadata:              map[string]string{"order_number": o.Number},
		IntegrationIdentifier: stripe.String(integrationIdentifier),
	}

	// PINNED, never omitted. A delayed method settles days after the session that
	// ExpiresAt bound to the stock hold, so the units are released and re-sold
	// before the capture lands. Card is what a 30-minute hold can survive.
	params.PaymentMethodTypes = stripe.StringSlice([]string{"card"})

	if o.Email != "" {
		params.CustomerEmail = stripe.String(o.Email)
	}

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
// Stripe is asked because the checkout URL is not derivable from the session id
// goen stores. An error is returned rather than read as "not open": creating a
// second session on a failed check is the double charge.
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

// ExpireSession closes a Checkout Session at Stripe so nobody can pay a
// cancelled order on a tab they still have open.
//
// Whether money is in flight is Stripe's question: it expires an OPEN session
// and refuses anything else, and that refusal is returned rather than failing
// the cancellation, which has already committed.
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

// displayName is what Stripe's page calls a line: the product name with its
// variant label folded in, so the customer sees which one they are buying.
func displayName(l *Line) string {
	if l.Label == "" {
		return l.Name
	}
	return l.Name + "(" + l.Label + ")"
}

// VerifyWebhook checks a webhook's signature and returns the event. body must be
// the RAW bytes the signature was computed over.
func (g *Gateway) VerifyWebhook(body []byte, sigHeader string) (stripe.Event, error) {
	if !g.Enabled() {
		return stripe.Event{}, ErrDisabled
	}
	// The API-version check is off on purpose: Stripe upgrades an account's
	// version independently of this binary. The signature and the timestamp
	// tolerance are not relaxed.
	ev, err := webhook.ConstructEventWithOptions(body, sigHeader, g.webhookSecret,
		webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
	if err != nil {
		return stripe.Event{}, fmt.Errorf("%w: %w", ErrBadSignature, err)
	}
	return ev, nil
}

// captureEvents are the two events that can carry money goen must record. A
// delayed method's money clears as async_payment_succeeded and as nothing else,
// so a reader accepting only `completed` leaves the order unpaid for ever.
var captureEvents = map[stripe.EventType]bool{
	"checkout.session.completed":               true,
	"checkout.session.async_payment_succeeded": true,
}

// CaptureFrom reads a paid-checkout event into a [Capture], or reports that the
// event is not one goen acts on. Only payment_status `paid` is a capture: acting
// on the event TYPE alone marks orders paid for money that has not arrived.
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
var abandonedEvents = map[stripe.EventType]bool{
	"checkout.session.expired":              true,
	"checkout.session.async_payment_failed": true,
}

// AbandonedSessionFrom reports the session id of a Checkout Session that ended
// with no money. Without a reader for these, the payment row goen opened sits at
// requires_payment for ever.
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
// It is the one signal that a delayed payment method is in play, which the pin
// in [Gateway.StartSession] exists to prevent. It writes NOTHING: capturing
// would mark an order paid days early, and extending the hold is unbuilt.
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

// EventAge is how old Stripe says the event is, for logging.
func EventAge(ev *stripe.Event) string {
	if ev.Created == 0 {
		return "unknown"
	}
	return strconv.FormatInt(time.Now().Unix()-ev.Created, 10) + "s"
}
