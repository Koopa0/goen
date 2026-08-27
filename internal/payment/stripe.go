package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	stripe "github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/webhook"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web"
)

// Gateway is the only thing in goen that knows Stripe exists.
type Gateway struct {
	client        *stripe.Client
	webhookSecret string
	baseURL       string
}

// NewGateway wires Stripe. A blank secret key is not an error; a key with no
// webhook secret is, and leaves the endpoint that takes money open.
func NewGateway(secretKey, webhookSecret, baseURL string) (*Gateway, error) {
	if secretKey == "" {
		return &Gateway{baseURL: baseURL}, nil
	}
	if webhookSecret == "" {
		return nil, errors.New("payment: a Stripe secret key without a webhook secret " +
			"would leave /webhooks/stripe unauthenticated")
	}
	origin, _, ok := web.SiteOrigin(baseURL)
	if !ok {
		return nil, fmt.Errorf("payment: base URL %q is not usable for Stripe return URLs", baseURL)
	}
	return &Gateway{
		client:        stripe.NewClient(secretKey),
		webhookSecret: webhookSecret,
		baseURL:       origin,
	}, nil
}

// Enabled reports whether goen can actually take money.
func (g *Gateway) Enabled() bool { return g.client != nil }

// lineItem is one row on Stripe's page.
func lineItem(name string, unitCents, quantity int64) *stripe.CheckoutSessionCreateLineItemParams {
	return &stripe.CheckoutSessionCreateLineItemParams{
		Quantity: stripe.Int64(quantity),
		PriceData: &stripe.CheckoutSessionCreateLineItemPriceDataParams{
			Currency:   stripe.String(Currency),
			UnitAmount: stripe.Int64(unitCents),
			ProductData: &stripe.CheckoutSessionCreateLineItemPriceDataProductDataParams{
				Name: stripe.String(name),
			},
		},
	}
}

// StartSession creates a Checkout Session and returns its id and URL.
func (g *Gateway) StartSession(ctx context.Context, o *Order, attempt int32) (id, redirectURL string, err error) {
	if !g.Enabled() {
		return "", "", ErrDisabled
	}

	// Stripe's page must total what capture_payment will record, and
	// payments_capture_matches_order demands exactly order_amount_owed — which is
	// o.TotalCents, the total less the store credit spent on it.
	//
	// The difference from the lines runs BOTH ways: shipping and tax add, while a
	// coupon and store credit take away. Refusing the negative direction made
	// every reduced order unpayable, and free delivery over the advertised
	// threshold means any discount at all lands below the lines — with the
	// customer's credit already debited and their coupon already spent by the
	// checkout transaction, so the refusal arrived after the money was gone.
	//
	// A reduction is carried as ONE line for the whole order rather than spread
	// across the goods or sent as a Stripe coupon. Stripe rejects a negative
	// unit_amount outright, and a coupon is an object with its own lifetime to
	// create, look up and expire for a figure goen has already decided; itemising
	// the goods at a price nobody agreed to would make the receipt Stripe emails
	// disagree with the shop's own.
	var lineTotal int64
	for i := range o.Lines {
		lineTotal += o.Lines[i].UnitCents * int64(o.Lines[i].Quantity)
	}

	var items []*stripe.CheckoutSessionCreateLineItemParams
	if o.TotalCents >= lineTotal {
		items = make([]*stripe.CheckoutSessionCreateLineItemParams, 0, len(o.Lines)+1)
		for i := range o.Lines {
			l := &o.Lines[i]
			items = append(items, lineItem(displayName(l), l.UnitCents, int64(l.Quantity)))
		}
		if rest := o.TotalCents - lineTotal; rest > 0 {
			items = append(items, lineItem(i18n.T(ctx, i18n.KeyShippingAndTax), rest, 1))
		}
	} else {
		// One line naming the order, priced at what is actually owed. The
		// itemisation is on goen's own order page, which the confirmation links
		// to and which states the discount and the credit separately.
		items = []*stripe.CheckoutSessionCreateLineItemParams{
			lineItem(fmt.Sprintf(i18n.T(ctx, i18n.KeyPayOrderLine), o.Number), o.TotalCents, 1),
		}
	}

	params := &stripe.CheckoutSessionCreateParams{
		Mode:      stripe.String(string(stripe.CheckoutSessionModePayment)),
		LineItems: items,
		Locale:    stripe.String(i18n.FromContext(ctx).StripeTag()),
		// Where the browser lands, never where an order is marked paid.
		SuccessURL: stripe.String(g.baseURL + "/orders/" + url.PathEscape(o.Number) + "?paid=1"),
		CancelURL:  stripe.String(g.baseURL + "/orders/" + url.PathEscape(o.Number) + "/pay?cancelled=1"),
		// The session dies with the stock hold: a window measured from here
		// outlives the goods.
		ExpiresAt:             stripe.Int64(o.SessionExpiry().Unix()),
		ClientReferenceID:     stripe.String(o.Number),
		Metadata:              map[string]string{"order_number": o.Number},
		IntegrationIdentifier: stripe.String(integrationIdentifier),
	}

	// Pinned, never omitted: a delayed method settles days after ExpiresAt, so
	// the units are released and re-sold before the capture lands.
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

// ResumeSession reports where to send a customer who already has a session open,
// and whether it still is. An error is never read as "not open".
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

// ExpireSession closes a Checkout Session so nobody can pay a cancelled order on
// a tab they still have open. Stripe refuses anything but an open session, and
// that refusal is returned rather than swallowed.
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

// displayName is what Stripe's page calls a line.
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
	// version independently of this binary. Nothing else is relaxed.
	ev, err := webhook.ConstructEventWithOptions(body, sigHeader, g.webhookSecret,
		webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
	if err != nil {
		return stripe.Event{}, fmt.Errorf("%w: %w", ErrBadSignature, err)
	}
	return ev, nil
}

// captureEvents are the two events that can carry money. A delayed method's
// money clears as async_payment_succeeded and as nothing else.
var captureEvents = map[stripe.EventType]bool{
	"checkout.session.completed":               true,
	"checkout.session.async_payment_succeeded": true,
}

// CaptureFrom reads a paid-checkout event into a [Capture]. Only payment_status
// `paid` is a capture: the event type alone marks orders paid unfunded.
func CaptureFrom(ev *stripe.Event) (Capture, bool) {
	if ev == nil || ev.Data == nil || !captureEvents[ev.Type] {
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

// actionable reports whether goen has a branch for this event type. A type in
// here that yields nothing from every reader is a payload this binary could not
// read — not an event goen does not act on, and the two must not share an arm.
func actionable(ev *stripe.Event) bool {
	return ev != nil && (captureEvents[ev.Type] || abandonedEvents[ev.Type])
}

type webhookReadState uint8

const (
	webhookReadIgnored webhookReadState = iota
	webhookReadUnderstood
	webhookReadUnreadable
)

func classifyWebhook(ev *stripe.Event, understood bool) webhookReadState {
	if !actionable(ev) {
		return webhookReadIgnored
	}
	if understood {
		return webhookReadUnderstood
	}
	return webhookReadUnreadable
}

// AbandonedSessionFrom reports the session id of a Checkout Session that ended
// with no money.
func AbandonedSessionFrom(ev *stripe.Event) (string, bool) {
	if ev == nil || ev.Data == nil || !abandonedEvents[ev.Type] {
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
// finished while the money is still on its way: the one signal that a delayed
// payment method is in play.
func UnsettledSessionFrom(ev *stripe.Event) (string, bool) {
	if ev == nil || ev.Data == nil || ev.Type != "checkout.session.completed" {
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

// ObjectRef is the Stripe id an event is about, or "" when it cannot be read.
func ObjectRef(ev *stripe.Event) string {
	if ev == nil || ev.Data == nil {
		return ""
	}
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
