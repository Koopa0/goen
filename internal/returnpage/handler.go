package returnpage

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/orderaccess"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	access *orderaccess.Store
	store  *Store
	log    *slog.Logger
}

func NewHandler(s *Store, access *orderaccess.Store, log *slog.Logger) *Handler {
	if s == nil || access == nil || log == nil {
		panic("returnpage: NewHandler requires a store, an access check and a logger")
	}
	return &Handler{store: s, access: access, log: log}
}

func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	o, ok := h.ownOrder(w, r)
	if !ok {
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.Returns(
		pages.ReturnsMeta(r.Context(), o.Number), viewOf(o, nil, "")))
}

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
	u, signedIn := user.FromContext(ctx)
	if !signedIn {
		return uuid.NullUUID{}
	}
	id, err := uuid.Parse(u.ID)
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
		pages.ReturnsMeta(r.Context(), fresh.Number), viewOf(fresh, req, msg)))
}

// ownOrder: an order number is a per-day counter, so knowing one is not
// authorisation; a stranger gets the 404 an absent order gets.
func (h *Handler) ownOrder(w http.ResponseWriter, r *http.Request) (*Order, bool) {
	number := r.PathValue("number")
	ok, err := h.access.Allows(r, number)
	if err != nil {
		h.log.ErrorContext(r.Context(), "check order access", "order", number, "error", err)
	}
	if !ok {
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

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyOrderNotFound)}, "404",
		i18n.T(r.Context(), i18n.KeyOrderNotFound),
		i18n.T(r.Context(), i18n.KeyOrderNotYours)))
}

func viewOf(o *Order, req *Request, errMsg string) pages.ReturnsView {
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
			Status: returns.Status(e.Status), Reason: e.Reason,
			Resolution: e.Resolution, CreatedAt: e.CreatedAt, DecidedAt: e.DecidedAt,
		})
	}
	return v
}
