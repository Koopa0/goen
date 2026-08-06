package payment

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// maxWebhookBody bounds what Stripe may post. Its largest events are a few tens
// of kilobytes; 1 MiB is generous and still refuses a body meant to exhaust
// memory. The limit matters here more than on a form, because this endpoint is
// public and unauthenticated until the signature is checked — and the signature
// cannot be checked without first reading the whole body.
const maxWebhookBody = 1 << 20

// OrderAccess is the one thing this package needs from internal/cart: whether the
// browser making this request holds a token for the order it is asking about.
//
// Defined here, by the consumer. internal/cart returns its concrete *Store and knows
// nothing about this interface.
type OrderAccess interface {
	PlacedHere(ctx context.Context, r *http.Request, number string, secure bool) bool
}

// Handler serves the payment page and Stripe's webhook.
type Handler struct {
	access  OrderAccess
	store   *Store
	gateway *Gateway
	log     *slog.Logger
	secure  bool
}

// NewHandler wires the payment routes.
func NewHandler(s *Store, g *Gateway, access OrderAccess, log *slog.Logger, secureCookies bool) *Handler {
	if s == nil || g == nil || access == nil || log == nil {
		panic("payment: NewHandler requires a store, a gateway, an access check and a logger")
	}
	return &Handler{store: s, gateway: g, access: access, log: log, secure: secureCookies}
}

// Page shows what is owed and the button that starts the payment.
//
// GET /orders/{number}/pay
func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	o, ok := h.payableOrder(w, r, number)
	if !ok {
		return
	}
	// Paid, or funded by store credit and owing nothing. Both mean there is nothing
	// to pay, and a pay page offering a NT$0 button is a page whose button cannot
	// work — Stripe refuses a zero-amount session.
	if o.Paid || o.FullyFunded() {
		// o.Number and not the path value: this string came back from the
		// database, so it provably matches orders_number_format and cannot
		// steer the redirect anywhere.
		http.Redirect(w, r, "/orders/"+o.Number, http.StatusSeeOther)
		return
	}

	view := pages.PayView{
		Number:     o.Number,
		TotalCents: o.TotalCents,
		Email:      o.Email,
		Enabled:    h.gateway.Enabled(),
		Cancelled:  r.URL.Query().Get("cancelled") == "1",
	}
	for i := range o.Lines {
		l := &o.Lines[i]
		view.Lines = append(view.Lines, pages.PayLine{
			Name: l.Name, Label: l.Label, UnitCents: l.UnitCents, Quantity: l.Quantity,
		})
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Pay(pages.PayMeta(r.Context(), o.Number), view))
}

// Start creates the Stripe Checkout Session and sends the customer to it.
//
// POST /orders/{number}/pay
//
// A plain form post with a 303 to Stripe. There is no JavaScript in this path
// and no amount in the request body — the figure is recomputed from the order.
func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	o, ok := h.payableOrder(w, r, number)
	if !ok {
		return
	}
	if o.Paid {
		http.Redirect(w, r, "/orders/"+o.Number, http.StatusSeeOther)
		return
	}
	// Nothing left to pay — store credit or a full discount covered it. Sending this
	// to Stripe would either be refused (a zero-amount session) or, worse, charge
	// somebody for an order they have already funded.
	if o.FullyFunded() {
		http.Redirect(w, r, "/orders/"+o.Number, http.StatusSeeOther)
		return
	}
	if !h.gateway.Enabled() {
		h.notice(w, r, http.StatusServiceUnavailable,
			i18n.T(r.Context(), i18n.KeyPayOffTitle),
			i18n.T(r.Context(), i18n.KeyPayOffHeading),
			i18n.T(r.Context(), i18n.KeyPayDisabled))
		return
	}

	// One order, one live session. Every POST here used to create a fresh Stripe
	// session and a fresh requires_payment row — open_payment dedupes on
	// (order_id, provider_ref) and each session brings its own id — so two tabs
	// were two real charges, and payments_one_capture_per_order refused the
	// second capture only AFTER the money had left the customer's account: the
	// webhook 500s, Stripe retries forever, nothing refunds.
	attempt, err := h.store.PaymentAttempt(r.Context(), number, o.TotalCents)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read payment attempt", "order", number, "error", err)
		h.serverError(w, r)
		return
	}
	if attempt.SessionID != "" {
		h.resume(w, r, o, attempt.SessionID)
		return
	}

	// Below Stripe's own floor there is no session goen can honestly create: one
	// padded out to thirty minutes would outlive the stock it is being paid for,
	// which is the defect this whole expiry rework exists to close. The customer
	// is told the hold has gone rather than sent to a checkout for goods that may
	// be sold from under them while they type a card number.
	if !o.HoldCoversASession(time.Now()) {
		h.log.InfoContext(r.Context(), "refusing to open a checkout on a lapsed stock hold",
			"order", number, "hold_expires_at", o.HoldExpiresAt)
		h.notice(w, r, http.StatusConflict,
			i18n.T(r.Context(), i18n.KeyPayRefusedTitle),
			i18n.T(r.Context(), i18n.KeyPayRefusedTitle),
			i18n.T(r.Context(), i18n.KeyPayRefusedBody))
		return
	}

	sessionID, redirectURL, err := h.gateway.StartSession(r.Context(), o, attempt.Prior)
	if err != nil {
		h.log.ErrorContext(r.Context(), "start checkout session", "order", number, "error", err)
		h.serverError(w, r)
		return
	}

	// The payment row is written BEFORE the redirect. If this fails the customer
	// never reaches Stripe, which is the safe direction: a session nobody paid
	// costs nothing, whereas a capture with no row to land on is money goen
	// cannot attribute to an order.
	if err := h.store.OpenPayment(r.Context(), number, sessionID, o.TotalCents); err != nil {
		h.log.ErrorContext(r.Context(), "open payment", "order", number, "error", err)
		h.serverError(w, r)
		return
	}

	h.toCheckout(w, r, number, redirectURL)
}

// resume sends a customer back to the Checkout Session they already have.
//
// A session Stripe has finished with is never replaced with a second one: paid
// and still-processing sessions may have money in flight, and an expired one
// means the stock hold behind the order has gone. The order page is where all
// three states are readable, so that is where they go.
func (h *Handler) resume(w http.ResponseWriter, r *http.Request, o *Order, sessionID string) {
	redirectURL, open, err := h.gateway.ResumeSession(r.Context(), sessionID)
	if err != nil {
		// Deliberately NOT falling through to create one. A session goen cannot
		// read is a session that may be about to take money, and opening a second
		// checkout on that doubt is the double charge this path exists to prevent.
		h.log.ErrorContext(r.Context(), "read the open checkout session",
			"order", o.Number, "session", sessionID, "error", err)
		h.serverError(w, r)
		return
	}
	if !open {
		h.log.InfoContext(r.Context(), "the open checkout session is finished at Stripe",
			"order", o.Number, "session", sessionID)
		//nolint:gosec // G710: o.Number came back from the database and not from
		// the request, so it provably matches orders_number_format and cannot
		// steer the redirect anywhere. The taint analyser cannot see that it
		// crossed a query on the way in.
		http.Redirect(w, r, "/orders/"+o.Number, http.StatusSeeOther)
		return
	}
	h.toCheckout(w, r, o.Number, redirectURL)
}

// toCheckout sends the customer to Stripe, having checked that is where the URL
// actually goes.
//
// The URL comes from the Stripe client rather than from the request, so this is
// not defending against the visitor — it is defending against a misconfigured or
// substituted API base turning goen's checkout into an open redirect that asks
// for a card number.
func (h *Handler) toCheckout(w http.ResponseWriter, r *http.Request, number, redirectURL string) {
	if !checkoutHost(redirectURL) {
		h.log.ErrorContext(r.Context(), "refusing a checkout redirect off Stripe",
			"order", number, "url", redirectURL)
		h.serverError(w, r)
		return
	}
	//nolint:gosec // G710: checkoutHost above restricts the target to Stripe;
	// the taint analyser cannot see through the helper. TestCheckoutHostOnly
	// AcceptsStripe is what keeps that claim true.
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

// stripeCheckoutHosts is where a Checkout Session may legitimately live.
var stripeCheckoutHosts = map[string]bool{
	"checkout.stripe.com": true,
	// Sessions on a custom domain a merchant has configured in Stripe. goen
	// uses none today; when it does, the domain is added here AND to the CSP's
	// form-action, or the browser blocks what this permits.
}

// checkoutHost reports whether a redirect target is a Stripe checkout page.
func checkoutHost(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	return stripeCheckoutHosts[u.Hostname()]
}

// Webhook is where an order actually becomes paid.
//
// POST /webhooks/stripe
//
// Nothing else in goen marks a payment succeeded. The customer's return to
// success_url is a page, not a fact: anyone can request it.
//
// The status codes are a protocol, not decoration. Stripe retries on anything
// that is not 2xx, so:
//   - a bad signature is 400 — retrying will not fix a forgery
//   - an event goen does not act on is 200 — it is recorded and done
//   - a database failure is 500 — Stripe SHOULD try again
func (h *Handler) Webhook(w http.ResponseWriter, r *http.Request) {
	if !h.gateway.Enabled() {
		http.Error(w, "payments are not configured", http.StatusServiceUnavailable)
		return
	}

	// Raw bytes: the signature is computed over exactly what was sent, so the
	// body must not be decoded and re-encoded before it is checked.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		http.Error(w, "could not read request body", http.StatusRequestEntityTooLarge)
		return
	}

	ev, err := h.gateway.VerifyWebhook(body, r.Header.Get("Stripe-Signature"))
	if err != nil {
		// Logged at warn, not error: an unverified post to a public endpoint is
		// the internet, not an outage. It is worth seeing if it becomes a flood.
		h.log.WarnContext(r.Context(), "rejected stripe webhook", "error", err)
		http.Error(w, "signature verification failed", http.StatusBadRequest)
		return
	}

	// What the event asks goen to do, if anything. Deciding this BEFORE the
	// transaction keeps the transaction to the two writes that must agree.
	capture, isCapture := CaptureFrom(&ev)
	abandonedSession, isAbandoned := AbandonedSessionFrom(&ev)
	var apply func(context.Context, *Store) error
	var number string
	var unknownSession, cancelledOrder bool
	switch {
	case isAbandoned:
		// A checkout that ended with no money — expired, or an asynchronous
		// method that failed. The payment row goen opened stayed at
		// requires_payment forever without this, so nothing could tell an
		// abandoned session from one still in flight.
		apply = func(ctx context.Context, st *Store) error {
			return st.CancelSession(ctx, abandonedSession)
		}
	case isCapture:
		apply = func(ctx context.Context, st *Store) error {
			n, captureErr := st.Capture(ctx, &capture)
			if errors.Is(captureErr, ErrOrderCancelled) {
				// Money for an order somebody called off while this session was
				// still open at Stripe. Swallowed inside the transaction for the
				// same reason the unknown session below is — the record of what
				// arrived is the only thing reconciliation has to work from, and
				// returning would roll the claim back and lose it.
				//
				// Answering 500 and letting Stripe retry would be worse than
				// useless: 'cancelled' is a terminal fulfilment status, so no
				// retry can ever succeed, and three days of 5xx on this endpoint
				// gets it disabled — which would stop every OTHER order being
				// captured. It is recorded, reported at ERROR with the session id,
				// and refunded by a human.
				cancelledOrder = true
				number = n
				return nil
			}
			if errors.Is(captureErr, ErrNotFound) {
				// A session goen never opened: a webhook for another
				// integration sharing this endpoint, or a session from a
				// database that has since been reset.
				//
				// Swallowed INSIDE the transaction rather than returned,
				// because returning it would roll the claim back and lose the
				// event from the audit trail. Capture wrote nothing, so
				// committing records what arrived and nothing else.
				unknownSession = true
				return nil
			}
			number = n
			return captureErr
		}
	}

	claimed, err := h.store.ProcessWebhook(r.Context(), &WebhookEvent{
		ID: ev.ID, Type: string(ev.Type), ObjectRef: ObjectRef(&ev), Payload: body,
	}, apply)
	switch {
	case err != nil:
		// 500 so Stripe retries. Because the claim rolled back with the effect,
		// that retry actually reprocesses rather than being told it is a
		// duplicate — which is the whole reason the two writes share a
		// transaction.
		h.log.ErrorContext(r.Context(), "process stripe webhook",
			"event", ev.ID, "type", ev.Type, "error", err)
		http.Error(w, "could not process event", http.StatusInternalServerError)
		return

	case !claimed:
		// A redelivery of an event already processed.
		h.log.InfoContext(r.Context(), "stripe webhook already processed", "event", ev.ID)
		w.WriteHeader(http.StatusOK)
		return
	}

	switch {
	case isAbandoned:
		h.log.InfoContext(r.Context(), "checkout session ended without payment",
			"event", ev.ID, "type", ev.Type, "session", abandonedSession)
	case cancelledOrder:
		// ERROR, not warn: real money is at Stripe against an order nobody will
		// ship, and only a person can put it back.
		h.log.ErrorContext(r.Context(), "money arrived for a cancelled order — refund it by hand",
			"event", ev.ID, "order", number, "session", capture.SessionID,
			"amount_cents", capture.AmountRecv)
	case unknownSession:
		// Recorded and not acted on. 200, because retrying will never find a
		// payment goen did not open.
		h.log.WarnContext(r.Context(), "capture for a session goen never opened",
			"event", ev.ID, "session", capture.SessionID)
	case isCapture:
		h.log.InfoContext(r.Context(), "payment captured",
			"order", number, "event", ev.ID, "amount_cents", capture.AmountRecv)
	default:
		// Recorded, not acted on. goen subscribes to more than it handles so the
		// history is complete, and so adding a handler later has the events.
		h.log.InfoContext(r.Context(), "stripe webhook recorded",
			"event", ev.ID, "type", ev.Type, "age", EventAge(&ev))
	}
	w.WriteHeader(http.StatusOK)
}

// payableOrder loads an order the requester is allowed to pay for, or writes the
// refusal itself and reports false.
//
// The gate is the same one the confirmation page uses, and for the same reason:
// an order number is a per-day counter, so knowing one is not authorisation. A
// stranger gets the 404 an absent order gets, because a 403 would confirm the
// number is real.
func (h *Handler) payableOrder(w http.ResponseWriter, r *http.Request, number string) (*Order, bool) {
	if !h.access.PlacedHere(r.Context(), r, number, h.secure) && !h.ownedBySignedInUser(r, number) {
		h.notFound(w, r)
		return nil, false
	}
	o, err := h.store.Order(r.Context(), number)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.notFound(w, r)
			return nil, false
		}
		h.log.ErrorContext(r.Context(), "read order for payment", "order", number, "error", err)
		h.serverError(w, r)
		return nil, false
	}
	// Only an order still waiting for money may be paid. A cancelled order with
	// an open payment page is how a customer pays for something nobody will ship.
	if o.Fulfillment != "pending" && !o.Paid {
		h.notice(w, r, http.StatusConflict,
			i18n.T(r.Context(), i18n.KeyPayRefusedTitle),
			i18n.T(r.Context(), i18n.KeyPayRefusedTitle),
			i18n.T(r.Context(), i18n.KeyPayRefusedBody))
		return nil, false
	}
	return o, true
}

func (h *Handler) ownedBySignedInUser(r *http.Request, number string) bool {
	u, ok := account.FromContext(r.Context())
	if !ok {
		return false
	}
	owns, err := h.store.OrderBelongsTo(r.Context(), number, u.ID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "check order ownership", "error", err)
		return false
	}
	return owns
}

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	h.notice(w, r, http.StatusNotFound,
		i18n.T(r.Context(), i18n.KeyOrderNotFound),
		i18n.T(r.Context(), i18n.KeyOrderNotFound),
		i18n.T(r.Context(), i18n.KeyOrderNotYours))
}

func (h *Handler) notice(w http.ResponseWriter, r *http.Request, status int, title, heading, body string) {
	web.Render(w, r, h.log, status, pages.Notice(
		layouts.Page{Title: title}, "", heading, body))
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	h.notice(w, r, http.StatusInternalServerError,
		i18n.T(r.Context(), i18n.KeyTryAgainTitle),
		i18n.T(r.Context(), i18n.KeyTryAgainTitle),
		i18n.T(r.Context(), i18n.KeyLoggedTryAgain))
}
