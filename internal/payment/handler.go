package payment

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	stripe "github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

const maxWebhookBody = 1 << 20

var errObsoleteSessionUncertain = errors.New("payment: obsolete checkout may still take money")

type webhookUnreconciledCause string

const (
	webhookUnreadableEvent       webhookUnreconciledCause = "unreadable_event"
	webhookUnattributedCapture   webhookUnreconciledCause = "unattributed_capture"
	webhookCancelledOrderCapture webhookUnreconciledCause = "cancelled_order_capture"
	webhookRefusedCapture        webhookUnreconciledCause = "refused_capture"
	webhookUnsettledSession      webhookUnreconciledCause = "unsettled_session"
)

func webhookUnreconciled(cause webhookUnreconciledCause, detail string) string {
	return string(cause) + ": " + detail
}

type webhookOutcome struct {
	event               *stripe.Event
	capture             Capture
	readState           webhookReadState
	abandonedSession    string
	unsettledSession    string
	number              string
	isAbandoned         bool
	isCapture           bool
	isUnsettled         bool
	cancelledOrder      bool
	unattributedCapture bool
	refusedCapture      bool
}

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
	if o.Paid || o.FullyFunded() {
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
	if h.handleExistingAttempt(w, r, o, attempt) {
		return
	}

	if !o.holdCoversSession {
		h.log.InfoContext(r.Context(), "refusing to open a checkout on a lapsed stock hold",
			"order", number, "hold_expires_at", o.HoldExpiresAt)
		h.paymentConflict(w, r)
		return
	}

	sessionID, err := h.gateway.StartSession(r.Context(), o, attempt.Prior)
	if err != nil {
		h.log.ErrorContext(r.Context(), "start checkout session", "order", number, "error", err)
		h.serverError(w, r)
		return
	}

	// Before the redirect: a capture with no row to land on is unattributable.
	if err := h.store.OpenPayment(r.Context(), number, sessionID, o.TotalCents); err != nil {
		// Stripe has created a payable remote object, whether this is a stable
		// business refusal, a deadlock victim or an uncertain transport result.
		// Close every session whose local admission was not confirmed, and make
		// that expired idempotency generation durable before allowing a retry.
		if cleanupErr := h.expireRejectedSession(r, o, sessionID); cleanupErr != nil {
			h.log.ErrorContext(r.Context(), "clean up checkout rejected after creation",
				"order", number, "session", sessionID, "open_error", err,
				"cleanup_error", cleanupErr)
			h.serverError(w, r)
			return
		}
		if errors.Is(err, ErrNotOpenable) {
			h.paymentConflict(w, r)
			return
		}
		h.log.ErrorContext(r.Context(), "open payment", "order", number, "error", err)
		h.serverError(w, r)
		return
	}

	// Do not trust Create's response here. Stripe replays the original body for
	// an idempotency key even if that Session has since expired; a fresh retrieve
	// is what prevents recording and redirecting to a dead session.
	h.resume(w, r, o, sessionID, o.TotalCents)
}

// handleExistingAttempt either responds for a live/uncertain attempt or safely
// retires an explicitly expired obsolete one so Start may create a replacement.
// The boolean reports whether an HTTP response has already been written.
func (h *Handler) handleExistingAttempt(
	w http.ResponseWriter,
	r *http.Request,
	o *Order,
	attempt *Attempt,
) bool {
	if attempt.NeedsReconciliation {
		h.log.WarnContext(r.Context(), "refusing a second checkout while captured money needs reconciliation",
			"order", o.Number)
		h.paymentConflict(w, r)
		return true
	}
	if attempt.SessionID == "" {
		return false
	}
	if attempt.MatchesOwed {
		h.resume(w, r, o, attempt.SessionID, attempt.IntendedAmountCents)
		return true
	}

	retireErr := h.retireObsoleteSession(
		r, o.Number, attempt.SessionID, attempt.IntendedAmountCents,
	)
	if retireErr == nil {
		return false
	}
	if errors.Is(retireErr, errObsoleteSessionUncertain) {
		h.log.WarnContext(r.Context(), "refusing to replace an uncertain obsolete checkout",
			"order", o.Number, "session", attempt.SessionID, "error", retireErr)
		h.paymentConflict(w, r)
		return true
	}
	h.log.ErrorContext(r.Context(), "record expired checkout at an obsolete amount",
		"order", o.Number, "session", attempt.SessionID, "error", retireErr)
	h.serverError(w, r)
	return true
}

// retireObsoleteSession closes a session whose amount no longer matches the
// order. Only Stripe's explicit open/expired states make replacement safe. A
// complete, unknown or unreadable state remains a possible capture and is
// classified separately from a local persistence failure. Retirement survives a
// client disconnect but has one short, shared budget: a cancelled local write
// would leave goen calling a session payable after Stripe closed it.
func (h *Handler) retireObsoleteSession(
	r *http.Request, number, sessionID string, intendedAmountCents int64,
) error {
	const timeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), timeout)
	defer cancel()

	_, status, err := h.gateway.ResumeSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("%w: retrieve session %s: %w", errObsoleteSessionUncertain, sessionID, err)
	}

	switch status {
	case stripe.CheckoutSessionStatusOpen:
		if err := h.gateway.ExpireSession(ctx, sessionID); err != nil {
			return fmt.Errorf("%w: expire session %s: %w", errObsoleteSessionUncertain, sessionID, err)
		}
	case stripe.CheckoutSessionStatusExpired:
		// A previous request already established the provider fact.
	case stripe.CheckoutSessionStatusComplete:
		if err := h.store.recordCompletePayment(
			ctx, number, sessionID, intendedAmountCents,
		); err != nil {
			return fmt.Errorf("record complete obsolete session %s: %w", sessionID, err)
		}
		return fmt.Errorf("%w: session %s is complete", errObsoleteSessionUncertain, sessionID)
	default:
		return fmt.Errorf("%w: session %s has status %q", errObsoleteSessionUncertain, sessionID, status)
	}

	return h.store.cancelExpiredPayment(ctx, sessionID)
}

// expireRejectedSession closes a remote session that never gained a confirmed
// local admission and persists its terminal generation. Cleanup survives a
// client disconnect but has one short, shared budget.
func (h *Handler) expireRejectedSession(r *http.Request, o *Order, sessionID string) error {
	const timeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), timeout)
	defer cancel()

	expireErr := h.gateway.ExpireSession(ctx, sessionID)
	if expireErr == nil {
		return h.store.recordExpiredPayment(ctx, o.Number, sessionID, o.TotalCents)
	}

	// The Session may have crossed a terminal boundary before the expire call.
	// Read that provider fact instead of treating every refusal as uncertainty.
	// Expired is the only state recorded as cancelled. Complete does not say
	// whether money moved, so it consumes the generation in a reconciliation
	// state that a later capture or operator resolution can converge.
	_, status, retrieveErr := h.gateway.ResumeSession(ctx, sessionID)
	if retrieveErr != nil {
		return errors.Join(expireErr, retrieveErr)
	}
	switch status {
	case stripe.CheckoutSessionStatusExpired:
		return h.store.recordExpiredPayment(ctx, o.Number, sessionID, o.TotalCents)
	case stripe.CheckoutSessionStatusComplete:
		return h.store.recordCompletePayment(ctx, o.Number, sessionID, o.TotalCents)
	case stripe.CheckoutSessionStatusOpen:
		return expireErr
	default:
		return fmt.Errorf("expire rejected session %s: %w (retrieve returned status %q)",
			sessionID, expireErr, status)
	}
}

// resume sends a customer back to an open Checkout Session. An explicitly
// expired one closes locally; a complete or unreadable one is never replaced
// while captured money may still be in flight.
func (h *Handler) resume(
	w http.ResponseWriter, r *http.Request, o *Order, sessionID string, intendedAmountCents int64,
) {
	redirectURL, status, err := h.gateway.ResumeSession(r.Context(), sessionID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read the open checkout session",
			"order", o.Number, "session", sessionID, "error", err)
		h.serverError(w, r)
		return
	}
	switch status {
	case stripe.CheckoutSessionStatusOpen:
		h.toCheckout(w, r, o.Number, redirectURL)
		return
	case stripe.CheckoutSessionStatusExpired:
		// Only an explicit provider expiry is safe to make cancelled locally.
		// A complete Session may have paid money whose webhook is still in flight.
		if err := h.store.cancelExpiredPayment(r.Context(), sessionID); err != nil {
			h.log.ErrorContext(r.Context(), "record checkout expired at Stripe",
				"order", o.Number, "session", sessionID, "error", err)
			h.serverError(w, r)
			return
		}
		h.log.InfoContext(r.Context(), "the open checkout session is finished at Stripe",
			"order", o.Number, "session", sessionID, "status", status)
	case stripe.CheckoutSessionStatusComplete:
		if err := h.store.recordCompletePayment(
			r.Context(), o.Number, sessionID, intendedAmountCents,
		); err != nil {
			h.log.ErrorContext(r.Context(), "record checkout complete at Stripe",
				"order", o.Number, "session", sessionID, "error", err)
			h.serverError(w, r)
			return
		}
		h.log.InfoContext(r.Context(), "the checkout session completed at Stripe; waiting for its webhook",
			"order", o.Number, "session", sessionID)
	default:
		h.log.ErrorContext(r.Context(), "Stripe returned an unknown checkout session state",
			"order", o.Number, "session", sessionID, "status", status)
		h.serverError(w, r)
		return
	}

	//nolint:gosec // G710: o.Number came back from the database and not from
	// the request, so it provably matches orders_number_format and cannot
	// steer the redirect anywhere. The taint analyser cannot see that it
	// crossed a query on the way in.
	http.Redirect(w, r, "/orders/"+o.Number, http.StatusSeeOther)
}

// toCheckout sends the customer to Stripe, having checked that is where the URL
// actually goes.
func (h *Handler) toCheckout(w http.ResponseWriter, r *http.Request, number, redirectURL string) {
	if !checkoutRedirectURL(redirectURL) {
		h.log.ErrorContext(r.Context(), "refusing an unsafe Stripe checkout redirect",
			"order", number, "url_bytes", len(redirectURL))
		h.serverError(w, r)
		return
	}
	//nolint:gosec // G710: checkoutRedirectURL above requires an absolute HTTPS
	// URL with a host and without userinfo; the taint analyser cannot see through
	// the helper. TestCheckoutRedirectURL holds that boundary.
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

// Webhook is the automatic door where an order becomes paid. The only other
// door is an audited admin attribution of a complete Session awaiting a money
// outcome; a browser return remains no evidence. Stripe retries anything that
// is not 2xx, so a forgery or a signed event with no safe durable identity is
// 400, an ignored event, one whose business object is unreadable, or one a
// retry cannot apply is 200 (the latter two with a durable alarm), and a
// database failure is 500.
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
		if errors.Is(err, ErrBadSignature) {
			h.log.WarnContext(r.Context(), "rejected stripe webhook signature", "error", err)
			http.Error(w, "signature verification failed", http.StatusBadRequest)
			return
		}
		h.log.ErrorContext(r.Context(), "rejected malformed signed stripe webhook", "error", err)
		http.Error(w, "signed event was not usable", http.StatusBadRequest)
		return
	}

	capture, isCapture := CaptureFrom(&ev)
	abandonedSession, isAbandoned := AbandonedSessionFrom(&ev)
	unsettledSession, isUnsettled := UnsettledSessionFrom(&ev)
	outcome := &webhookOutcome{
		event: &ev, capture: capture,
		readState:        classifyWebhook(&ev, isCapture || isAbandoned || isUnsettled),
		abandonedSession: abandonedSession, unsettledSession: unsettledSession,
		isAbandoned: isAbandoned, isCapture: isCapture, isUnsettled: isUnsettled,
	}

	claimed, err := h.store.processWebhook(r.Context(), &webhookEvent{
		ID: ev.ID, Type: string(ev.Type), ObjectRef: ObjectRef(&ev), Payload: body,
	}, outcome.apply())
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

	h.logWebhookOutcome(r.Context(), *outcome)
	w.WriteHeader(http.StatusOK)
}

// apply is the effect this event is allowed to have. nil means record-and-
// ignore: Stripe must not retry an event goen has no branch for.
func (o *webhookOutcome) apply() func(context.Context, *webhookTx) error {
	switch {
	case o.readState == webhookReadUnreadable:
		return func(ctx context.Context, tx *webhookTx) error {
			// A retry delivers the same bytes and can never make this payload
			// readable. Commit a durable alarm and answer 200 instead of turning
			// version skew into a retry storm that disables the endpoint.
			return tx.Unreconciled(ctx, webhookUnreconciled(webhookUnreadableEvent,
				"goen could not read a "+string(o.event.Type)+
					" it acts on: the payload is not the shape this binary expects"))
		}
	case o.isAbandoned:
		return func(ctx context.Context, tx *webhookTx) error {
			return tx.CancelSession(ctx)
		}
	case o.isUnsettled:
		return func(ctx context.Context, tx *webhookTx) error {
			// Money is still in flight. Retries cannot settle it, and a 5xx
			// would disable the endpoint. The stock hold cannot outlive this
			// window, so the claim must carry a durable alarm.
			return tx.Unreconciled(ctx, webhookUnreconciled(webhookUnsettledSession,
				"a delayed payment method completed a checkout — goen's stock hold cannot outlive it"))
		}
	case o.isCapture:
		return func(ctx context.Context, tx *webhookTx) error {
			n, captureErr := tx.Capture(ctx, o.capture)
			if errors.Is(captureErr, ErrOrderCancelled) {
				// Swallowed inside the transaction: returning would roll the
				// claim back and lose the only record that money arrived. The
				// event is marked UNRECONCILED in that same transaction — the
				// money is at Stripe, the goods are back on the shelf, and
				// somebody has to refund it by hand.
				o.cancelledOrder = true
				o.number = n
				return tx.Unreconciled(ctx, webhookUnreconciled(
					webhookCancelledOrderCapture,
					"money arrived for an order that was already cancelled"))
			}
			if errors.Is(captureErr, ErrNotFound) {
				// CaptureFrom already established that this is a paid, positive
				// Checkout Session. There is no order to guess: keep the 200 so the
				// same unresolvable bytes are not retried, and leave the session id
				// in object_ref for the person who must find the money at Stripe.
				o.unattributedCapture = true
				return tx.Unreconciled(ctx, webhookUnreconciled(
					webhookUnattributedCapture,
					"a paid Checkout Session has no payment row to attribute it to"))
			}
			if errors.Is(captureErr, errCaptureRefused) {
				// Stripe has already reported this session paid. A stable amount,
				// state, or schema invariant rejected it locally; retries carry the
				// same facts and cannot heal it. Preserve the reason in the event
				// transaction so admin health has something durable to act on.
				o.refusedCapture = true
				o.number = n
				return tx.Unreconciled(ctx, webhookUnreconciled(
					webhookRefusedCapture, captureErr.Error()))
			}
			o.number = n
			return captureErr
		}
	}
	return nil
}

func (h *Handler) logWebhookOutcome(ctx context.Context, outcome webhookOutcome) {
	ev := outcome.event
	switch {
	case outcome.isAbandoned:
		h.log.InfoContext(ctx, "checkout session ended without payment",
			"event", ev.ID, "type", ev.Type, "session", outcome.abandonedSession)
	case outcome.cancelledOrder:
		h.log.ErrorContext(ctx, "money arrived for a cancelled order — refund it by hand",
			"event", ev.ID, "order", outcome.number, "session", outcome.capture.SessionID,
			"amount_cents", outcome.capture.AmountRecv)
	case outcome.unattributedCapture:
		h.log.ErrorContext(ctx,
			"money arrived for a session goen cannot attribute — find it at Stripe",
			"event", ev.ID, "session", outcome.capture.SessionID)
	case outcome.refusedCapture:
		h.log.ErrorContext(ctx,
			"money arrived but local payment invariants refused it — reconcile or refund it",
			"event", ev.ID, "order", outcome.number, "session", outcome.capture.SessionID,
			"amount_cents", outcome.capture.AmountRecv)
	case outcome.isCapture:
		h.log.InfoContext(ctx, "payment captured",
			"order", outcome.number, "event", ev.ID, "amount_cents", outcome.capture.AmountRecv)
	case outcome.isUnsettled:
		// ERROR because the session pins card: this is a configuration change.
		h.log.ErrorContext(ctx,
			"a delayed payment method completed a checkout — goen's stock hold cannot outlive it",
			"event", ev.ID, "session", outcome.unsettledSession)
	case outcome.readState == webhookReadUnreadable:
		h.log.ErrorContext(ctx,
			"a stripe event goen acts on could not be read — check the endpoint's API version",
			"event", ev.ID, "type", ev.Type, "object", ObjectRef(ev), "age", EventAge(ev))
	default:
		h.log.InfoContext(ctx, "stripe webhook recorded",
			"event", ev.ID, "type", ev.Type, "age", EventAge(ev))
	}
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

func (h *Handler) paymentConflict(w http.ResponseWriter, r *http.Request) {
	h.notice(w, r, http.StatusConflict,
		i18n.T(r.Context(), i18n.KeyPayRefusedTitle),
		i18n.T(r.Context(), i18n.KeyPayRefusedTitle),
		i18n.T(r.Context(), i18n.KeyPayRefusedBody))
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	h.notice(w, r, http.StatusInternalServerError,
		i18n.T(r.Context(), i18n.KeyTryAgainTitle),
		i18n.T(r.Context(), i18n.KeyTryAgainTitle),
		i18n.T(r.Context(), i18n.KeyLoggedTryAgain))
}
