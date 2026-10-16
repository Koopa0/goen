package buyer

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	access *access.Store
	store  *Store
	log    *slog.Logger
	// findLimit bounds the order lookup, otherwise an oracle for the secret
	// half of a credential whose other half is guessable.
	findLimit *ratelimit.Limiter
	// findPerAddress bounds the lookup per submitted address as well as per
	// client: the order number is guessable, so the address is the only secret,
	// and a per-client bound alone is lifted by changing client.
	findPerAddress *ratelimit.Limiter
	// takesPayment is the cart's TakesPayment: false on a deployment with no
	// Stripe key, where the order page offers no way to pay.
	takesPayment bool
}

func NewHandler(s *Store, grants *access.Store, log *slog.Logger, findLimit *ratelimit.Limiter, takesPayment bool) *Handler {
	if s == nil || grants == nil || log == nil || findLimit == nil {
		panic("buyer: NewHandler requires a store, an access check, a logger and a lookup limiter")
	}
	return &Handler{
		store: s, access: grants, log: log, findLimit: findLimit, takesPayment: takesPayment,
		// Five lookups of one address at once, then five an hour, from
		// anywhere: a customer finding their own order needs one or two.
		findPerAddress: ratelimit.New(ratelimit.Config{
			Every: 12 * time.Minute, Burst: 5, TTL: time.Hour, MaxKeys: 65_536,
		}),
	}
}

func (h *Handler) FindOrderPage(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusOK, FindOrder(
		FindOrderMeta(r.Context()), FindOrderView{}))
}

// FindOrder writes the SAME cookie placing an order writes, and answers
// IDENTICALLY on every failure, since "that number exists but the address is
// wrong" hands over half a credential.
func (h *Handler) FindOrder(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	typed := strings.TrimSpace(r.PostFormValue("number"))
	number, _ := order.NormalizeNumber(typed) // "" when it names no order shape; the store refuses it
	addr := r.PostFormValue("email")

	// Bounded per IP and BEFORE the read: unbounded, this endpoint is an oracle
	// for the secret half of the pair.
	if retryAfter, ok := h.findLimit.Allow("findorder:" + ratelimit.ClientKey(r)); !ok {
		ratelimit.Refuse(r.Context(), w, retryAfter)
		return
	}
	// Keyed on the address as the lookup reads it, so case or spacing changes
	// are the same address; one longer than the policy names no order and is
	// not kept as a key.
	if key := email.Clean(addr); key != "" && len(key) <= email.MaxBytes {
		if retryAfter, ok := h.findPerAddress.Allow("findorder:" + key); !ok {
			ratelimit.Refuse(r.Context(), w, retryAfter)
			return
		}
	}

	found, err := h.store.OrderBelongsToEmail(r.Context(), number, addr)
	if err != nil {
		h.log.ErrorContext(r.Context(), "find order", "error", err)
		h.cartUnavailable(w, r)
		return
	}
	if !found {
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, FindOrder(
			FindOrderMeta(r.Context()), FindOrderView{
				Number: typed, Email: addr,
				Error: i18n.T(r.Context(), i18n.KeyFindOrderRefused),
			}))
		return
	}

	if err := h.rememberOrder(w, r, number); err != nil {
		h.findOrderGrantFailed(w, r)
		return
	}
	http.Redirect(w, r, "/orders/"+url.PathEscape(number), http.StatusSeeOther)
}

func (h *Handler) findOrderGrantFailed(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusInternalServerError, FindOrderGrantFailed(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyFindOrderGrantFailedTitle)}))
}

func (h *Handler) rememberOrder(w http.ResponseWriter, r *http.Request, number string) error {
	if err := h.access.Grant(w, r, number); err != nil {
		h.log.ErrorContext(r.Context(), "remember order", "error", err, "order", number)
		return err
	}
	return nil
}

func (h *Handler) OrderPage(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")

	// Anything but the browser that placed the order or the account that owns
	// it gets the same answer as an order that does not exist.
	if !h.allows(r, number) {
		if web.ReloadSameSite(w, r, h.log, payment.OrderReload) {
			return
		}
		web.Render(w, r, h.log, http.StatusNotFound, layouts.OrderNotFound(h.notFoundPage(r)))
		return
	}

	view, err := h.store.Order(r.Context(), number)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			web.Render(w, r, h.log, http.StatusNotFound, layouts.Notice(
				h.notFoundPage(r), "404", i18n.T(r.Context(), i18n.KeyOrderNotFound),
				i18n.T(r.Context(), i18n.KeyOrderGone)))
			return
		}
		h.log.ErrorContext(r.Context(), "read order", "error", err)
		h.cartUnavailable(w, r)
		return
	}
	view.Cancelled = r.URL.Query().Get("cancelled") == "1"
	view.PaymentRefreshURL = paymentReturnRefresh(r, &view)
	if view.PaymentRefreshURL != "" {
		w.Header().Set("Refresh", strconv.Itoa(paymentReturnRefreshSeconds)+"; url="+view.PaymentRefreshURL)
		view.PaymentRefreshSeconds = paymentReturnRefreshSeconds
		view.PaymentRefreshChecks = paymentReturnRefreshChecks
	}
	view.ShowWarrantyLink = h.ownedBySignedInUser(r, number)
	view.PaymentsEnabled = h.takesPayment
	web.Render(w, r, h.log, http.StatusOK, Order(OrderMeta(r.Context(), view.Number), &view))
}

const (
	paymentReturnRefreshSeconds = 5
	paymentReturnRefreshChecks  = 3
)

// A browser return is not payment evidence. Keep that hint when bounded checks
// end so a delayed webhook cannot turn it into an invitation to pay again.
func paymentReturnRefresh(r *http.Request, view *OrderView) string {
	view.PaymentReturnHint = r.URL.Query().Get("paid") == "1"
	if !view.AwaitingPayment() || !view.PaymentReturnHint {
		return ""
	}
	attempt := 0
	if raw := r.URL.Query().Get("confirmation"); raw != "" {
		if raw == "done" {
			view.PaymentConfirmationPending = true
			return ""
		}
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed >= paymentReturnRefreshChecks {
			return ""
		}
		attempt = parsed
	}
	next := "/orders/" + url.PathEscape(view.Number) + "?paid=1&confirmation="
	if attempt < paymentReturnRefreshChecks-1 {
		next += strconv.Itoa(attempt + 1)
	} else {
		next += "done"
	}
	return next
}

func (h *Handler) ownedBySignedInUser(r *http.Request, number string) bool {
	owns, err := h.access.OwnedBySignedInUser(r, number)
	if err != nil {
		h.log.ErrorContext(r.Context(), "check order ownership", "error", err)
	}
	return owns
}

func (h *Handler) CancelOrder(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if !h.allows(r, number) {
		h.notFound(w, r)
		return
	}

	err := h.store.CancelOrder(r.Context(), number)
	switch {
	case err == nil:
		http.Redirect(w, r, "/orders/"+url.PathEscape(number)+"?cancelled=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotCancellable):
		// 422, not a redirect: nothing was written.
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, layouts.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyCancelRefusedTitle)}, "",
			i18n.T(r.Context(), i18n.KeyCancelRefusedTitle),
			i18n.T(r.Context(), i18n.KeyCancelRefusedBody), layouts.NoticeActions{
				Primary:   layouts.NoticeLink{Href: "/orders/" + url.PathEscape(number), Label: i18n.KeyBackToOrder},
				Secondary: layouts.NoticeLink{Href: "/contact", Label: i18n.KeyContact},
			}))
	default:
		h.log.ErrorContext(r.Context(), "cancel order", "error", err)
		h.cartUnavailable(w, r)
	}
}

func (h *Handler) ReturnPage(w http.ResponseWriter, r *http.Request) {
	o, ok := h.ownOrder(w, r)
	if !ok {
		return
	}
	view := viewOf(r.Context(), o, nil, nil)
	view.Filed = r.URL.Query().Get("filed") == "1"
	web.Render(w, r, h.log, http.StatusOK, Returns(
		ReturnsMeta(r.Context(), o.Number), view))
}

func (h *Handler) SubmitReturn(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	o, ok := h.ownOrder(w, r)
	if !ok {
		return
	}

	draft := returnDraftFromForm(r, o)
	req, refusals := draft.request(o)
	if len(refusals) > 0 {
		h.reject(w, r, o, draft, refusals)
		return
	}
	err := h.store.OpenReturn(r.Context(), o.Number, returnRequester(r.Context()), req)
	h.respondToOpen(w, r, o, draft, err)
}

type returnDraft struct {
	Reason     string
	Quantities map[string]string
}

func returnDraftFromForm(r *http.Request, o *ReturnOrder) *returnDraft {
	draft := &returnDraft{Reason: r.PostFormValue("reason"), Quantities: make(map[string]string, len(o.Lines))}
	for _, line := range o.Lines {
		draft.Quantities[line.ID] = r.PostFormValue("qty_" + line.ID)
	}
	return draft
}

func (d *returnDraft) request(o *ReturnOrder) (*ReturnRequest, []web.FieldRefusal) {
	req := &ReturnRequest{Reason: d.Reason, Lines: make(map[string]int32, len(o.Lines))}
	var refusals []web.FieldRefusal
	chosen := false
	quantityRefused := false
	for _, line := range o.Lines {
		raw := d.Quantities[line.ID]
		if raw == "" {
			continue
		}
		quantity, err := strconv.ParseInt(raw, 10, 32)
		key := i18n.Key("")
		switch {
		case err != nil || quantity < 0:
			key = i18n.KeyReturnQuantityInvalid
		case quantity > int64(line.Returnable):
			key = i18n.KeyReturnTooMany
		default:
			req.Lines[line.ID] = int32(quantity)
			chosen = chosen || quantity > 0
		}
		if key != "" {
			quantityRefused = true
			refusals = append(refusals, web.FieldRefusal{Field: "qty_" + line.ID, MessageKey: key})
		}
	}
	if key := returnReasonMessage(d.Reason); key != "" {
		refusals = append(refusals, web.FieldRefusal{Field: "reason", MessageKey: key})
	}
	if !chosen && !quantityRefused {
		refusals = append(refusals, web.FieldRefusal{MessageKey: i18n.KeyReturnInvalid})
	}
	return req, refusals
}

func returnReasonMessage(reason string) i18n.Key {
	switch result := validateReturnReason(reason); result {
	case reasonValid:
		return ""
	case reasonTooLong:
		return i18n.KeyReturnReasonTooLong
	case reasonUnsupportedControls:
		return i18n.KeyReturnReasonUnsupportedControls
	default:
		panic("buyer: unknown reason validation: " + string(result))
	}
}

func returnRequester(ctx context.Context) uuid.NullUUID {
	u, signedIn := user.FromContext(ctx)
	if !signedIn {
		return uuid.NullUUID{}
	}
	id, err := uuid.Parse(u.ID)
	return uuid.NullUUID{UUID: id, Valid: err == nil}
}

func (h *Handler) respondToOpen(
	w http.ResponseWriter, r *http.Request, o *ReturnOrder, draft *returnDraft, err error,
) {
	switch {
	case err == nil:
		http.Redirect(w, r, "/orders/"+url.PathEscape(o.Number)+"/return?filed=1", http.StatusSeeOther)
	case errors.Is(err, ErrReturnOpen):
		h.reject(w, r, o, draft, []web.FieldRefusal{{MessageKey: i18n.KeyReturnAlreadyOpen}})
	case errors.Is(err, ErrNotReturnable):
		h.reject(w, r, o, draft, []web.FieldRefusal{{MessageKey: i18n.KeyReturnNothingShort}})
	case errors.Is(err, ErrReturnTooMany):
		h.reject(w, r, o, draft, []web.FieldRefusal{{MessageKey: i18n.KeyReturnTooMany}})
	case errors.Is(err, ErrReturnAccountErased):
		h.reject(w, r, o, draft, []web.FieldRefusal{{MessageKey: i18n.KeyReturnAccountErased}})
	case errors.Is(err, ErrReturnInvalid):
		h.reject(w, r, o, draft, []web.FieldRefusal{{MessageKey: i18n.KeyReturnInvalid}})
	default:
		h.log.ErrorContext(r.Context(), "open return request", "order", o.Number, "error", err)
		web.Render(w, r, h.log, http.StatusInternalServerError, layouts.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
			i18n.T(r.Context(), i18n.KeyTryAgainTitle),
			i18n.T(r.Context(), i18n.KeyLoggedTryAgain)))
	}
}

func (h *Handler) reject(w http.ResponseWriter, r *http.Request, o *ReturnOrder, draft *returnDraft, refusals []web.FieldRefusal) {
	fresh, err := h.store.ReturnOrder(r.Context(), o.Number)
	if err != nil {
		h.log.ErrorContext(r.Context(), "refresh order after return refusal", "order", o.Number, "error", err)
		web.Render(w, r, h.log, http.StatusInternalServerError, layouts.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
			i18n.T(r.Context(), i18n.KeyTryAgainTitle),
			i18n.T(r.Context(), i18n.KeyLoggedTryAgain)))
		return
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, Returns(
		ReturnsMeta(r.Context(), fresh.Number), viewOf(r.Context(), fresh, draft, returnRefusals(fresh, draft, refusals))))
}

// ownOrder: an order number is a per-day counter, so knowing one is not
// authorisation; a stranger gets the 404 an absent order gets.
func (h *Handler) ownOrder(w http.ResponseWriter, r *http.Request) (*ReturnOrder, bool) {
	number := r.PathValue("number")
	if !h.allows(r, number) {
		h.notFound(w, r)
		return nil, false
	}
	o, err := h.store.ReturnOrder(r.Context(), number)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.notFound(w, r)
			return nil, false
		}
		h.log.ErrorContext(r.Context(), "read order for return", "order", number, "error", err)
		web.Render(w, r, h.log, http.StatusInternalServerError, layouts.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
			i18n.T(r.Context(), i18n.KeyTryAgainTitle),
			i18n.T(r.Context(), i18n.KeyLoggedTryAgain)))
		return nil, false
	}
	return o, true
}

func (h *Handler) allows(r *http.Request, number string) bool {
	ok, err := h.access.Allows(r, number)
	if err != nil {
		h.log.ErrorContext(r.Context(), "check order access", "order", number, "error", err)
	}
	return ok
}

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusNotFound, layouts.OrderNotFound(h.notFoundPage(r)))
}

func (h *Handler) notFoundPage(r *http.Request) layouts.Page {
	return layouts.Page{Title: i18n.T(r.Context(), i18n.KeyOrderNotFound)}
}

// cartUnavailable is the failure page the lookup, the order page and cancelling
// share with the cart's routes.
func (h *Handler) cartUnavailable(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusInternalServerError, layouts.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
		i18n.T(r.Context(), i18n.KeyTryAgainTitle),
		i18n.T(r.Context(), i18n.KeyCartUnavailable)))
}

func viewOf(ctx context.Context, o *ReturnOrder, draft *returnDraft, refusals []web.FieldRefusal) ReturnsView {
	v := ReturnsView{
		Number: o.Number, HasOpen: o.HasOpen, HasDraft: draft != nil,
		Pickup: o.Pickup, Now: o.Now,
	}
	for _, p := range o.Shipments {
		v.Shipments = append(v.Shipments, OrderShipment{
			DeliveredAt: p.DeliveredAt, RescissionEnds: p.RescissionEnds, RescissionDays: p.RescissionDays,
		})
	}
	for i := range o.Lines {
		l := &o.Lines[i]
		line := ReturnsLine{
			ID: l.ID, SKU: l.SKU, Name: l.Name, Label: l.Label,
			UnitCents: l.UnitCents, Returnable: l.Returnable,
		}
		if draft != nil {
			line.Quantity = draft.Quantities[l.ID]
		}
		for _, refusal := range refusals {
			if refusal.Field == line.Field() {
				line.Refusal = i18n.T(ctx, refusal.MessageKey)
			}
		}
		v.Lines = append(v.Lines, line)
	}
	if draft != nil {
		v.Reason = draft.Reason
	}
	for _, refusal := range refusals {
		switch refusal.Field {
		case "":
			v.FormRefusal = i18n.T(ctx, refusal.MessageKey)
		case "reason":
			v.ReasonRefusal = i18n.T(ctx, refusal.MessageKey)
		}
	}
	for i := range o.Existing {
		e := &o.Existing[i]
		v.Existing = append(v.Existing, ReturnsExisting{
			Status: returns.Status(e.Status), Reason: e.Reason,
			Resolution: e.Resolution, CreatedAt: e.CreatedAt, DecidedAt: e.DecidedAt,
		})
	}
	return v
}

func returnRefusals(o *ReturnOrder, draft *returnDraft, refusals []web.FieldRefusal) []web.FieldRefusal {
	if o.HasOpen {
		return nil
	}
	for _, refusal := range refusals {
		switch refusal.MessageKey {
		case i18n.KeyReturnTooMany:
			_, fresh := draft.request(o)
			if len(fresh) > 0 {
				return fresh
			}
		case i18n.KeyReturnNothingShort:
			_, fresh := draft.request(o)
			return append(refusals, fresh...)
		}
	}
	return refusals
}
