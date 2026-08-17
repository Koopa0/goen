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

const maxWebhookBody = 1 << 20

// OrderAccess reports whether this browser holds a token for the order.
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
func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	o, ok := h.payableOrder(w, r, number)
	if !ok {
		return
	}
	if o.Paid || o.FullyFunded() {
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

// Start creates the Stripe Checkout Session and sends the customer to it with a
// 303. The figure comes off the order; no amount is read from the request.
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

	// Before the redirect: a capture with no row to land on is unattributable.
	if err := h.store.OpenPayment(r.Context(), number, sessionID, o.TotalCents); err != nil {
		h.log.ErrorContext(r.Context(), "open payment", "order", number, "error", err)
		h.serverError(w, r)
		return
	}

	h.toCheckout(w, r, number, redirectURL)
}

// resume sends a customer back to the Checkout Session they already have. One
// Stripe has finished with, or one goen cannot read, is never replaced.
func (h *Handler) resume(w http.ResponseWriter, r *http.Request, o *Order, sessionID string) {
	redirectURL, open, err := h.gateway.ResumeSession(r.Context(), sessionID)
	if err != nil {
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
func (h *Handler) toCheckout(w http.ResponseWriter, r *http.Request, number, redirectURL string) {
	if !checkoutHost(redirectURL) {
		h.log.ErrorContext(r.Context(), "refusing a checkout redirect off Stripe",
			"order", number, "url", redirectURL)
		h.serverError(w, r)
		return
	}
	//nolint:gosec // G710: checkoutHost above restricts the target to Stripe;
	// the taint analyser cannot see through the helper. TestCheckoutHostOnlyAcceptsStripe
	// AcceptsStripe is what keeps that claim true.
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

// stripeCheckoutHosts is where a Checkout Session may live. A host added here
// must also be added to the CSP's form-action.
var stripeCheckoutHosts = map[string]bool{
	"checkout.stripe.com": true,
}

// checkoutHost reports whether a redirect target is a Stripe checkout page.
func checkoutHost(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	return stripeCheckoutHosts[u.Hostname()]
}

// Webhook is where an order becomes paid; nothing else in goen marks a payment
// succeeded. Stripe retries anything that is not 2xx, so a forgery is 400, an
// event goen does not act on is 200, and a database failure is 500.
func (h *Handler) Webhook(w http.ResponseWriter, r *http.Request) {
	if !h.gateway.Enabled() {
		http.Error(w, "payments are not configured", http.StatusServiceUnavailable)
		return
	}

	// Raw bytes: the signature is over exactly what was sent, so decoding and
	// re-encoding the JSON first breaks verification.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		http.Error(w, "could not read request body", http.StatusRequestEntityTooLarge)
		return
	}

	ev, err := h.gateway.VerifyWebhook(body, r.Header.Get("Stripe-Signature"))
	if err != nil {
		h.log.WarnContext(r.Context(), "rejected stripe webhook", "error", err)
		http.Error(w, "signature verification failed", http.StatusBadRequest)
		return
	}

	capture, isCapture := CaptureFrom(&ev)
	abandonedSession, isAbandoned := AbandonedSessionFrom(&ev)
	unsettledSession, isUnsettled := UnsettledSessionFrom(&ev)
	var apply func(context.Context, *Store) error
	var number string
	var unknownSession, cancelledOrder bool
	switch {
	case isAbandoned:
		apply = func(ctx context.Context, st *Store) error {
			return st.CancelSession(ctx, abandonedSession)
		}
	case isCapture:
		apply = func(ctx context.Context, st *Store) error {
			n, captureErr := st.Capture(ctx, &capture)
			if errors.Is(captureErr, ErrOrderCancelled) {
				// Swallowed inside the transaction: returning would roll the
				// claim back and lose the only record that money arrived.
				cancelledOrder = true
				number = n
				return nil
			}
			if errors.Is(captureErr, ErrNotFound) {
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
		// 500 so Stripe retries; the claim rolled back with the effect.
		h.log.ErrorContext(r.Context(), "process stripe webhook",
			"event", ev.ID, "type", ev.Type, "error", err)
		http.Error(w, "could not process event", http.StatusInternalServerError)
		return

	case !claimed:
		h.log.InfoContext(r.Context(), "stripe webhook already processed", "event", ev.ID)
		w.WriteHeader(http.StatusOK)
		return
	}

	switch {
	case isAbandoned:
		h.log.InfoContext(r.Context(), "checkout session ended without payment",
			"event", ev.ID, "type", ev.Type, "session", abandonedSession)
	case cancelledOrder:
		h.log.ErrorContext(r.Context(), "money arrived for a cancelled order — refund it by hand",
			"event", ev.ID, "order", number, "session", capture.SessionID,
			"amount_cents", capture.AmountRecv)
	case unknownSession:
		h.log.WarnContext(r.Context(), "capture for a session goen never opened",
			"event", ev.ID, "session", capture.SessionID)
	case isCapture:
		h.log.InfoContext(r.Context(), "payment captured",
			"order", number, "event", ev.ID, "amount_cents", capture.AmountRecv)
	case isUnsettled:
		// ERROR because the session pins card: this is a configuration change.
		h.log.ErrorContext(r.Context(),
			"a delayed payment method completed a checkout — goen's stock hold cannot outlive it",
			"event", ev.ID, "session", unsettledSession)
	default:
		h.log.InfoContext(r.Context(), "stripe webhook recorded",
			"event", ev.ID, "type", ev.Type, "age", EventAge(&ev))
	}
	w.WriteHeader(http.StatusOK)
}

// payableOrder loads an order the requester may pay for, or writes the refusal
// itself and reports false. A stranger gets a 404, because a 403 confirms the
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
	// An open payment page on a cancelled order takes money for no goods.
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
