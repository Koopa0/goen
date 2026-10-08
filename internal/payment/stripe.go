package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
	sandbox       bool
}

const maxStripeIDCharacters = 255

// Payment-mode Checkout Sessions accept at most 100 line_items;
// goen's shipping-and-tax line is one of them.
const maxCheckoutLineItems = 100

var errInvalidStripeResponse = errors.New("payment: Stripe returned an invalid response")

// ValidStripeID reports whether id is safe to retain as a durable provider
// identity. Stripe IDs are opaque, so this deliberately does not freeze their
// prefixes; it only enforces the database-sized identity contract and excludes
// whitespace and controls that can create aliases or forge log lines.
func ValidStripeID(id string) bool {
	if id == "" || !utf8.ValidString(id) || utf8.RuneCountInString(id) > maxStripeIDCharacters {
		return false
	}
	return !strings.ContainsFunc(id, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r)
	})
}

// checkoutRedirectURL validates the URL Stripe tells a browser to visit. A
// custom Checkout domain is still Stripe-hosted, so hostname allowlists reject
// a supported configuration; the security boundary is an absolute HTTPS URL
// with a real host and no userinfo.
func checkoutRedirectURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.IsAbs() && strings.EqualFold(u.Scheme, "https") &&
		u.Hostname() != "" && u.User == nil
}

// KeyMode is which Stripe environment a key belongs to, read from its prefix.
type KeyMode int

const (
	// KeyUnknown is a key whose prefix names no environment. It is neither
	// assured to be a test key nor refused as a live one.
	KeyUnknown KeyMode = iota
	KeyTest
	KeyLive
)

// ClassifyKey is the one place goen reads a Stripe key's environment: the
// sandbox notice and the startup posture check must agree on it. Surrounding
// whitespace is not part of the key: a padded live key is still live.
func ClassifyKey(apiKey string) KeyMode {
	apiKey = strings.TrimSpace(apiKey)
	for _, prefix := range []string{"sk_test_", "rk_test_", "rkcs_test_"} {
		if strings.HasPrefix(apiKey, prefix) {
			return KeyTest
		}
	}
	for _, prefix := range []string{"sk_live_", "rk_live_"} {
		if strings.HasPrefix(apiKey, prefix) {
			return KeyLive
		}
	}
	return KeyUnknown
}

// NewGateway wires Stripe. A blank API key is not an error; a key with no
// webhook secret is, and leaves the endpoint that takes money open.
func NewGateway(apiKey, webhookSecret, baseURL string) (*Gateway, error) {
	return newGateway(apiKey, webhookSecret, baseURL, &stripe.BackendConfig{})
}

func newGateway(apiKey, webhookSecret, baseURL string, backend *stripe.BackendConfig) (*Gateway, error) {
	if apiKey == "" {
		return &Gateway{baseURL: baseURL}, nil
	}
	if webhookSecret == "" {
		return nil, errors.New("payment: a Stripe API key without a webhook secret " +
			"would leave /webhooks/stripe unauthenticated")
	}
	origin, _, ok := web.SiteOrigin(baseURL)
	if !ok {
		return nil, fmt.Errorf("payment: base URL %q is not usable for Stripe return URLs", baseURL)
	}
	return &Gateway{
		client:        newStripeClient(apiKey, backend),
		webhookSecret: webhookSecret,
		baseURL:       origin,
		sandbox:       ClassifyKey(apiKey) == KeyTest,
	}, nil
}

func (g *Gateway) Enabled() bool { return g.client != nil }

func (g *Gateway) Sandbox() bool { return g.sandbox }

func lineItem(name string, unitCents, quantity int64) *stripe.CheckoutSessionCreateLineItemParams {
	return &stripe.CheckoutSessionCreateLineItemParams{
		Quantity: new(quantity),
		PriceData: &stripe.CheckoutSessionCreateLineItemPriceDataParams{
			Currency:   stripe.String(Currency),
			UnitAmount: new(unitCents),
			ProductData: &stripe.CheckoutSessionCreateLineItemPriceDataProductDataParams{
				Name: stripe.String(name),
			},
		},
	}
}

// StartSession creates a Checkout Session and returns its durable id. The
// redirect URL is deliberately not returned: the handler records the id first,
// then [Gateway.ResumeSession] retrieves and validates a fresh URL. This also
// means an unusable URL in Create's response cannot orphan an already-created
// payable Session before its id is recorded.
func (g *Gateway) StartSession(ctx context.Context, o *Order, attempt int32) (string, error) {
	if !g.Enabled() {
		return "", ErrDisabled
	}

	// Stripe's page must total what capture_payment will record, and
	// payments_capture_matches_order demands exactly order_amount_after_credit — which is
	// o.TotalCents, the total less the store credit spent on it. The difference
	// from the lines runs BOTH ways: shipping and tax add, while a coupon and
	// store credit take away.
	//
	// Reductions and orders exceeding Stripe's line-item limit use one line
	// for the whole order. Stripe rejects a negative unit_amount, and repricing
	// goods would make its receipt disagree with the shop's own.
	var lineTotal int64
	items := make([]*stripe.CheckoutSessionCreateLineItemParams, 0, len(o.Lines)+1)
	for i := range o.Lines {
		l := &o.Lines[i]
		lineTotal += l.UnitCents * int64(l.Quantity)
		items = append(items, lineItem(displayName(l), l.UnitCents, int64(l.Quantity)))
	}
	if rest := o.TotalCents - lineTotal; rest > 0 {
		items = append(items, lineItem(i18n.T(ctx, i18n.KeyShippingAndTax), rest, 1))
	}
	if o.TotalCents < lineTotal || len(items) > maxCheckoutLineItems {
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
		ExpiresAt:             new(o.SessionExpiry().Unix()),
		ClientReferenceID:     stripe.String(o.Number),
		Metadata:              map[string]string{"order_number": o.Number},
		IntegrationIdentifier: stripe.String(integrationIdentifier),
	}

	// Deliberate exception to Stripe's dynamic-payment-method default: a delayed
	// method settles days after ExpiresAt, so the units are released and can be
	// re-sold before the capture lands. Supporting one requires a different stock
	// reservation model, not removing this field.
	params.PaymentMethodTypes = stripe.StringSlice([]string{"card"})

	if o.Email != "" {
		params.CustomerEmail = stripe.String(o.Email)
	}

	params.SetIdempotencyKey(SessionKey(o.ID, o.TotalCents, attempt))

	sess, err := g.client.V1CheckoutSessions.Create(ctx, params)
	if err != nil {
		return "", fmt.Errorf("create checkout session for order %s: %w", o.Number, err)
	}
	if sess == nil || !ValidStripeID(sess.ID) {
		return "", fmt.Errorf("%w: create checkout session returned an invalid session id",
			errInvalidStripeResponse)
	}
	return sess.ID, nil
}

// ResumeSession reports the provider's current state and, for an open session,
// where to send the customer. A fresh retrieve is intentional: replaying a
// Stripe idempotency key can return the original create response after the
// underlying Session has expired.
func (g *Gateway) ResumeSession(
	ctx context.Context, sessionID string,
) (redirectURL string, status stripe.CheckoutSessionStatus, err error) {
	if !g.Enabled() {
		return "", "", ErrDisabled
	}
	if !ValidStripeID(sessionID) {
		return "", "", errors.New("payment: cannot retrieve an invalid Stripe session id")
	}
	sess, err := g.client.V1CheckoutSessions.Retrieve(ctx, sessionID, nil)
	if err != nil {
		return "", "", fmt.Errorf("read checkout session %s: %w", sessionID, err)
	}
	if sess == nil || !ValidStripeID(sess.ID) || sess.ID != sessionID {
		return "", "", fmt.Errorf("%w: retrieve checkout session %s returned a different or invalid session id",
			errInvalidStripeResponse, sessionID)
	}
	if sess.Status != stripe.CheckoutSessionStatusOpen {
		return "", sess.Status, nil
	}
	if !checkoutRedirectURL(sess.URL) {
		return "", "", fmt.Errorf("%w: open checkout session %s returned an unsafe redirect URL",
			errInvalidStripeResponse, sessionID)
	}
	return sess.URL, sess.Status, nil
}

// ExpireSession closes a Checkout Session so nobody can pay a cancelled order on
// a tab they still have open. Stripe refuses anything but an open session, and
// that refusal is returned rather than swallowed.
func (g *Gateway) ExpireSession(ctx context.Context, sessionID string) error {
	if !g.Enabled() {
		return ErrDisabled
	}
	if !ValidStripeID(sessionID) {
		return errors.New("payment: cannot expire an invalid Stripe session id")
	}
	sess, err := g.client.V1CheckoutSessions.Expire(ctx, sessionID,
		&stripe.CheckoutSessionExpireParams{})
	if err != nil {
		return fmt.Errorf("expire checkout session %s: %w", sessionID, err)
	}
	if sess == nil || !ValidStripeID(sess.ID) || sess.ID != sessionID {
		return fmt.Errorf("%w: expire checkout session %s returned a different or invalid session id",
			errInvalidStripeResponse, sessionID)
	}
	return nil
}

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
	if !ValidStripeID(ev.ID) {
		return stripe.Event{}, fmt.Errorf("%w: webhook carried an invalid event id",
			errInvalidStripeResponse)
	}
	return ev, nil
}

// captureEvents are the two events that can carry money. A delayed method's
// money clears as async_payment_succeeded and as nothing else.
var captureEvents = map[stripe.EventType]bool{
	stripe.EventTypeCheckoutSessionCompleted:             true,
	stripe.EventTypeCheckoutSessionAsyncPaymentSucceeded: true,
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
	if !ValidStripeID(sess.ID) || sess.AmountTotal <= 0 {
		return Capture{}, false
	}
	c := Capture{SessionID: sess.ID, AmountRecv: sess.AmountTotal, Currency: string(sess.Currency)}
	if pi := sess.PaymentIntent; pi != nil {
		if ValidStripeID(pi.ID) {
			c.PaymentIntentID = pi.ID
		}
		c.CardBrand, c.CardLast4 = cardOf(pi.LatestCharge)
	}
	return c, true
}

// cardOf is the card brand and last four of a charge Stripe reported, or
// nothing for a charge that is absent or is not a card.
func cardOf(charge *stripe.Charge) (brand, last4 string) {
	if charge == nil || charge.PaymentMethodDetails == nil || charge.PaymentMethodDetails.Card == nil {
		return "", ""
	}
	card := charge.PaymentMethodDetails.Card
	return string(card.Brand), card.Last4
}

// CardFacts reads the card brand and last four of what a PaymentIntent paid with:
// one retrieve with latest_charge expanded, because a checkout event names the
// intent and never carries its charge. Used only when the event itself had no
// charge; the answer is display only and never decides whether money arrived.
func (g *Gateway) CardFacts(ctx context.Context, paymentIntentID string) (brand, last4 string, err error) {
	if !g.Enabled() {
		return "", "", ErrDisabled
	}
	if !ValidStripeID(paymentIntentID) {
		return "", "", errors.New("payment: cannot retrieve an invalid Stripe payment intent id")
	}
	params := &stripe.PaymentIntentRetrieveParams{}
	params.AddExpand("latest_charge")
	pi, err := g.client.V1PaymentIntents.Retrieve(ctx, paymentIntentID, params)
	if err != nil {
		return "", "", fmt.Errorf("read payment intent %s: %w", paymentIntentID, err)
	}
	if pi == nil || pi.ID != paymentIntentID {
		return "", "", fmt.Errorf("%w: retrieve payment intent %s returned a different or invalid intent",
			errInvalidStripeResponse, paymentIntentID)
	}
	brand, last4 = cardOf(pi.LatestCharge)
	// A value read for display must not become a capture the database refuses:
	// payments_last4_format would turn paid money into an unreconciled event.
	if brand == "" || len(brand) > 32 || len(last4) != 4 || strings.Trim(last4, "0123456789") != "" {
		return "", "", nil
	}
	return brand, last4, nil
}

// abandonedEvents are the two ways a Checkout Session ends with no money.
var abandonedEvents = map[stripe.EventType]bool{
	stripe.EventTypeCheckoutSessionExpired:            true,
	stripe.EventTypeCheckoutSessionAsyncPaymentFailed: true,
}

// actionable reports whether goen has a branch for this event type. A type in
// here that yields nothing from every reader is a payload this binary could not
// read — not an event goen does not act on, and the two must not share an arm.
func actionable(ev *stripe.Event) bool {
	return ev != nil && (captureEvents[ev.Type] || abandonedEvents[ev.Type] ||
		ev.Type == stripe.EventTypeRefundFailed)
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

func AbandonedSessionFrom(ev *stripe.Event) (string, bool) {
	if ev == nil || ev.Data == nil || !abandonedEvents[ev.Type] {
		return "", false
	}
	var sess stripe.CheckoutSession
	if err := json.Unmarshal(ev.Data.Raw, &sess); err != nil {
		return "", false
	}
	if !ValidStripeID(sess.ID) {
		return "", false
	}
	return sess.ID, true
}

// refundFailure is a refund Stripe reports failed after accepting it: a bank
// can return a card refund days later, and the money goes back to the Stripe
// balance.
type refundFailure struct {
	refundID string
	// reason is Stripe's failure_reason, or "" when it sent none or one that is
	// not a plain code: it reaches an operator's screen and a log line.
	reason string
}

var refundFailureReason = regexp.MustCompile(`^[a-z_]{1,64}$`)

func refundFailureFrom(ev *stripe.Event) (refundFailure, bool) {
	if ev == nil || ev.Data == nil || ev.Type != stripe.EventTypeRefundFailed {
		return refundFailure{}, false
	}
	var refund stripe.Refund
	if err := json.Unmarshal(ev.Data.Raw, &refund); err != nil {
		return refundFailure{}, false
	}
	if !ValidStripeID(refund.ID) {
		return refundFailure{}, false
	}
	f := refundFailure{refundID: refund.ID}
	if reason := string(refund.FailureReason); refundFailureReason.MatchString(reason) {
		f.reason = reason
	}
	return f, true
}

// UnsettledSessionFrom reports the session id of a checkout the customer
// finished while the money is still on its way: the one signal that a delayed
// payment method is in play.
func UnsettledSessionFrom(ev *stripe.Event) (string, bool) {
	if ev == nil || ev.Data == nil || ev.Type != stripe.EventTypeCheckoutSessionCompleted {
		return "", false
	}
	var sess stripe.CheckoutSession
	if err := json.Unmarshal(ev.Data.Raw, &sess); err != nil {
		return "", false
	}
	if !ValidStripeID(sess.ID) || sess.PaymentStatus != stripe.CheckoutSessionPaymentStatusUnpaid {
		return "", false
	}
	return sess.ID, true
}

// ObjectRef is the Stripe id an event is about, or "" when it cannot be read.
func ObjectRef(ev *stripe.Event) string {
	if ev == nil || ev.Data == nil {
		return ""
	}
	if id, ok := ev.Data.Object["id"].(string); ok && ValidStripeID(id) {
		return id
	}
	return ""
}

func EventAge(ev *stripe.Event) string {
	if ev.Created == 0 {
		return "unknown"
	}
	return strconv.FormatInt(time.Now().Unix()-ev.Created, 10) + "s"
}
