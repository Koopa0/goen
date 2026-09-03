package returns

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// OrderAccess reports whether the browser making this request holds a token for
// the order it is asking about.
type OrderAccess interface {
	PlacedHere(ctx context.Context, r *http.Request, number string, secure bool) bool
}

// Handler serves the customer's return form.
type Handler struct {
	access OrderAccess
	store  *Store
	log    *slog.Logger
	secure bool
}

// NewHandler wires the return routes.
func NewHandler(s *Store, access OrderAccess, log *slog.Logger, secureCookies bool) *Handler {
	if s == nil || access == nil || log == nil {
		panic("returns: NewHandler requires a store, an access check and a logger")
	}
	return &Handler{store: s, access: access, log: log, secure: secureCookies}
}

// Page serves GET /orders/{number}/return.
func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	o, ok := h.ownOrder(w, r)
	if !ok {
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Returns(
		pages.ReturnsMeta(r.Context(), o.Number), viewOf(r.Context(), o, nil, "")))
}

// Submit serves POST /orders/{number}/return.
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	o, ok := h.ownOrder(w, r)
	if !ok {
		return
	}

	req, err := returnRequestFromForm(r, o)
	if err != nil {
		h.reject(w, r, o, req, i18n.T(r.Context(), i18n.KeyReturnTooMany))
		return
	}
	err = h.store.Open(r.Context(), o.Number, returnRequester(r.Context()), req)
	h.respondToOpen(w, r, o, req, err)
}

func returnRequestFromForm(r *http.Request, o *Order) (*Request, error) {
	req := &Request{Reason: r.PostFormValue("reason"), Lines: map[string]int32{}}
	for i := range o.Lines {
		line := &o.Lines[i]
		raw := r.PostFormValue("qty_" + line.ID)
		if raw == "" {
			continue
		}
		quantity, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || quantity < 0 || quantity > int64(line.Returnable) {
			return req, ErrTooMany
		}
		req.Lines[line.ID] = int32(quantity)
	}
	return req, nil
}

func returnRequester(ctx context.Context) uuid.NullUUID {
	user, signedIn := account.FromContext(ctx)
	if !signedIn {
		return uuid.NullUUID{}
	}
	id, err := uuid.Parse(user.ID)
	return uuid.NullUUID{UUID: id, Valid: err == nil}
}

func (h *Handler) respondToOpen(
	w http.ResponseWriter, r *http.Request, o *Order, req *Request, err error,
) {
	switch {
	case err == nil:
		http.Redirect(w, r, "/orders/"+url.PathEscape(o.Number)+"/return?filed=1", http.StatusSeeOther)
	case errors.Is(err, ErrAlreadyOpen):
		h.reject(w, r, o, req, i18n.T(r.Context(), i18n.KeyReturnAlreadyOpen))
	case errors.Is(err, ErrNotReturnable):
		h.reject(w, r, o, req, i18n.T(r.Context(), i18n.KeyReturnNothingShort))
	case errors.Is(err, ErrTooMany):
		h.reject(w, r, o, req, i18n.T(r.Context(), i18n.KeyReturnTooMany))
	case errors.Is(err, ErrAccountErased):
		h.reject(w, r, o, req, i18n.T(r.Context(), i18n.KeyReturnAccountErased))
	case errors.Is(err, ErrInvalid):
		h.reject(w, r, o, req, i18n.T(r.Context(), i18n.KeyReturnInvalid))
	default:
		h.log.ErrorContext(r.Context(), "open return request", "order", o.Number, "error", err)
		web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
			i18n.T(r.Context(), i18n.KeyTryAgainTitle),
			i18n.T(r.Context(), i18n.KeyLoggedTryAgain)))
	}
}

// reject re-renders the form at 422 with what the customer typed still in it.
func (h *Handler) reject(w http.ResponseWriter, r *http.Request, o *Order, req *Request, msg string) {
	fresh, err := h.store.Order(r.Context(), o.Number)
	if err != nil {
		h.log.ErrorContext(r.Context(), "refresh order after return refusal", "order", o.Number, "error", err)
		web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
			i18n.T(r.Context(), i18n.KeyTryAgainTitle),
			i18n.T(r.Context(), i18n.KeyLoggedTryAgain)))
		return
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.Returns(
		pages.ReturnsMeta(r.Context(), fresh.Number), viewOf(r.Context(), fresh, req, msg)))
}

// ownOrder loads an order the requester may act on, or writes the refusal. An
// order number is a per-day counter, so knowing one is not authorisation: a
// stranger gets the 404 an absent order gets.
func (h *Handler) ownOrder(w http.ResponseWriter, r *http.Request) (*Order, bool) {
	number := r.PathValue("number")
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
		h.log.ErrorContext(r.Context(), "read order for return", "order", number, "error", err)
		web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
			i18n.T(r.Context(), i18n.KeyTryAgainTitle),
			i18n.T(r.Context(), i18n.KeyLoggedTryAgain)))
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
	web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyOrderNotFound)}, "404",
		i18n.T(r.Context(), i18n.KeyOrderNotFound),
		i18n.T(r.Context(), i18n.KeyOrderNotYours)))
}

// viewOf assembles the page; req is nil on a first render.
func viewOf(ctx context.Context, o *Order, req *Request, errMsg string) pages.ReturnsView {
	v := pages.ReturnsView{
		Number: o.Number, HasOpen: o.HasOpen, Error: errMsg,
	}
	for i := range o.Lines {
		l := &o.Lines[i]
		line := pages.ReturnsLine{
			ID: l.ID, SKU: l.SKU, Name: l.Name, Label: l.Label,
			UnitCents: l.UnitCents, Returnable: l.Returnable,
		}
		if req != nil {
			line.Chosen = req.Lines[l.ID]
		}
		v.Lines = append(v.Lines, line)
	}
	if req != nil {
		v.Reason = req.Reason
	}
	for i := range o.Existing {
		e := &o.Existing[i]
		v.Existing = append(v.Existing, pages.ReturnsExisting{
			StatusText: StatusLabel(ctx, e.Status), Reason: e.Reason,
			Resolution: e.Resolution, CreatedAt: e.CreatedAt, DecidedAt: e.DecidedAt,
		})
	}
	return v
}
